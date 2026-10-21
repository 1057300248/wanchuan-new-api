"""Exercise CI shell orchestration with offline command stubs, not real services."""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / "verify_repair_services.sh"
DSN_KEYS = ("TEST_MYSQL_DSN", "TEST_MYSQL_LOG_DSN", "TEST_POSTGRES_DSN", "TEST_POSTGRES_LOG_DSN",
            "TEST_DATABASE_DSN", "TEST_LOG_DATABASE_DSN", "AUDIT_MYSQL_DSN", "AUDIT_POSTGRES_DSN",
            "TEST_PERF_MYSQL_DSN", "TEST_PERF_POSTGRES_DSN", "TEST_FIXED_MYSQL_DSN", "TEST_FIXED_MYSQL_LOG_DSN",
            "TEST_FIXED_POSTGRES_DSN", "TEST_FIXED_POSTGRES_LOG_DSN", "TEST_DATABASE_COMPAT",
            "TEST_DATABASE_DIALECT", "TEST_TASK_DB_DIALECT")


class RepairServiceEnvironmentTests(unittest.TestCase):
    def run_script(self, dialect, reject_label="", creation_fails=False):
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            tools = root / "bin"
            tools.mkdir()
            trace = root / "trace.jsonl"
            stub = tools / "tool.py"
            stub.write_text("#!" + sys.executable + "\n" + '''import json, os, pathlib, sys
name = pathlib.Path(sys.argv[0]).name
args = sys.argv[1:]
record = {"tool": name, "args": args}
if name == "go" and args != ["version"]:
    record["env"] = {key: os.environ.get(key, "") for key in ''' + repr(DSN_KEYS) + '''}
with open(os.environ["MOCK_TRACE"], "a") as stream:
    stream.write(json.dumps(record) + "\\n")
if name == "git" and args == ["rev-parse", "HEAD"]:
    print(os.environ["SOURCE_SHA"])
elif name == "go" and args == ["version"]:
    print("offline orchestration stub; no Go tests executed")
elif name == "docker" and os.environ.get("MOCK_CREATE_FAIL") == "1" and any("repair_token_migration" in arg for arg in args):
    raise SystemExit(1)
elif name == "python3":
    assert args[0] == "ops/ci/check_go_test_report.py"
    assert args[1] == "--reject-skipped-descendants"
    assert len(args) > 3
    if pathlib.Path(args[2]).stem == os.environ.get("MOCK_REJECT_LABEL"):
        raise SystemExit(1)
''')
            stub.chmod(0o755)
            for name in ("git", "go", "docker", "python3"):
                (tools / name).symlink_to(stub)
            env = {"PATH": str(tools) + os.pathsep + os.environ["PATH"], "HOME": str(root),
                   "OUT": str(root / "evidence"), "SOURCE_SHA": "a" * 40, "DIALECT": dialect,
                   "MYSQL_ID": "b" * 64, "MYSQL_PORT": "13306", "PG_ID": "c" * 64, "PG_PORT": "15432",
                   "AUDIT_MYSQL_DSN": "untrusted-ambient-fixture", "AUDIT_POSTGRES_DSN": "untrusted-ambient-fixture",
                   "MOCK_TRACE": str(trace), "MOCK_REJECT_LABEL": reject_label,
                   "MOCK_CREATE_FAIL": "1" if creation_fails else "0"}
            result = subprocess.run(["bash", str(SCRIPT), "database"], cwd=root, env=env,
                                    text=True, capture_output=True, timeout=20)
            records = [json.loads(line) for line in trace.read_text().splitlines()]
            return result, records

    def test_real_database_selectors_receive_isolated_and_matching_dsns(self):
        for dialect, selected, inactive, port in (("mysql", "MYSQL", "POSTGRES", "13306"),
                                                 ("postgres", "POSTGRES", "MYSQL", "15432")):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect)
                self.assertEqual(0, result.returncode, result.stderr)
                tests = [row for row in rows if row["tool"] == "go" and row["args"] != ["version"]]
                gates = [row for row in rows if row["tool"] == "python3"]
                self.assertEqual(20, len(tests))
                self.assertEqual(len(tests), len(gates), "every suite must retain its strict evidence gate")
                for test, gate in zip(tests, gates):
                    label = Path(gate["args"][2]).stem
                    env = test["env"]
                    self.assertEqual("1", env["TEST_DATABASE_COMPAT"])
                    self.assertEqual(dialect, env["TEST_DATABASE_DIALECT"])
                    self.assertEqual("", env[f"TEST_{inactive}_DSN"])
                    self.assertEqual("", env[f"AUDIT_{inactive}_DSN"])
                    main = env["TEST_DATABASE_DSN"]
                    self.assertIn("127.0.0.1:" + port, main)
                    self.assertIn("/repair_compat?", main)
                    self.assertEqual(main, env[f"AUDIT_{selected}_DSN"])
                    actual = env[f"TEST_{selected}_DSN"]
                    if label == "token-migration":
                        self.assertIn("/repair_token_migration?", actual)
                        self.assertNotEqual(main, actual, "legacy migration may never reuse the startup database")
                    else:
                        self.assertEqual(main, actual, "migration override must not leak into another suite")
                    if label.startswith("performance-"):
                        self.assertEqual(main, env[f"TEST_PERF_{selected}_DSN"])
                    if label in ("native-responses", "converted-responses"):
                        self.assertEqual(main, env[f"TEST_FIXED_{selected}_DSN"])
                        self.assertEqual(env["TEST_LOG_DATABASE_DSN"], env[f"TEST_FIXED_{selected}_LOG_DSN"])
                    if label == "immediate-settlement":
                        self.assertEqual(dialect, env["TEST_TASK_DB_DIALECT"])
                creation = [row for row in rows if row["tool"] == "docker" and any("repair_token_migration" in arg for arg in row["args"])]
                self.assertEqual(1, len(creation))
                self.assertNotIn("IF NOT EXISTS", " ".join(creation[0]["args"]))
                self.assertNotIn("DROP", " ".join(creation[0]["args"]))

    def test_sqlite_cannot_inherit_external_audit_dsns(self):
        result, rows = self.run_script("sqlite")
        self.assertEqual(0, result.returncode, result.stderr)
        self.assertFalse(any(row["tool"] == "docker" for row in rows))
        for row in rows:
            if row["tool"] == "go" and row["args"] != ["version"]:
                for key in DSN_KEYS:
                    if key.endswith("DSN"):
                        self.assertEqual("", row["env"][key], key)

    def test_required_rejection_fails_job_but_keeps_independent_evidence(self):
        for label in ("token-migration", "token-audit"):
            with self.subTest(label=label):
                result, rows = self.run_script("postgres", reject_label=label)
                self.assertNotEqual(0, result.returncode)
                self.assertIn("Required repair contracts failed", result.stderr)
                self.assertTrue(any(row["tool"] == "python3" and Path(row["args"][2]).stem == "immediate-settlement" for row in rows))

    def test_existing_migration_database_cannot_be_reused_after_create_failure(self):
        for dialect in ("mysql", "postgres"):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect, creation_fails=True)
                self.assertNotEqual(0, result.returncode)
                self.assertFalse(any(row["tool"] == "go" and row["args"] != ["version"] for row in rows))


if __name__ == "__main__":
    unittest.main()
