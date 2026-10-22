"""Exercise CI shell orchestration with offline command stubs, not real services."""
import importlib.util
import json
import os
from pathlib import Path
import re
import subprocess
import sys
import tempfile
import unittest

SCRIPT = Path(__file__).resolve().parents[1] / "verify_repair_services.sh"
DSN_KEYS = ("TEST_MYSQL_DSN", "TEST_MYSQL_LOG_DSN", "TEST_POSTGRES_DSN", "TEST_POSTGRES_LOG_DSN",
            "TEST_DATABASE_DSN", "TEST_LOG_DATABASE_DSN", "AUDIT_MYSQL_DSN", "AUDIT_POSTGRES_DSN",
            "TEST_PERF_MYSQL_DSN", "TEST_PERF_POSTGRES_DSN", "TEST_FIXED_MYSQL_DSN", "TEST_FIXED_MYSQL_LOG_DSN",
            "TEST_FIXED_POSTGRES_DSN", "TEST_FIXED_POSTGRES_LOG_DSN", "TEST_DATABASE_COMPAT",
            "TEST_DATABASE_DIALECT", "TEST_TASK_DB_DIALECT", "TEST_RUNTIME_REDIS",
            "TEST_REDIS_DSN", "TEST_REDIS_ADDR", "TEST_TASK_REDIS_ADDR",
            "TEST_FINALIZATION_MODEL_MYSQL_DSN", "TEST_FINALIZATION_MODEL_POSTGRES_DSN",
            "TEST_FINALIZATION_SERVICE_MYSQL_DSN", "TEST_FINALIZATION_SERVICE_POSTGRES_DSN",
            "TEST_REFUND_OPS_MYSQL_DSN", "TEST_REFUND_OPS_POSTGRES_DSN", "TEST_REFUND_QUEUE_PRESSURE",
            "TEST_FINALIZATION_CACHE_MYSQL_DSN", "TEST_FINALIZATION_CACHE_POSTGRES_DSN",
            "TEST_SETTLEMENT_DEADLINE_REDIS_ADDR", "TEST_SETTLEMENT_DEADLINE_REDIS_DISPOSABLE")


class RepairServiceEnvironmentTests(unittest.TestCase):
    def run_script(self, dialect, reject_label="", creation_fails=False, *, mode="database",
                   redis_version="8.8.0", redis_info="# Server\r\nredis_version:8.8.0\r\n",
                   redis_info_fails=False, finalization_creation_fails="", refund_operations_creation_fails=False):
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
elif name == "docker" and os.environ.get("MOCK_FINALIZATION_CREATE_FAIL") and any("repair_finalization_" + os.environ["MOCK_FINALIZATION_CREATE_FAIL"] in arg for arg in args):
    raise SystemExit(1)
elif name == "docker" and os.environ.get("MOCK_REFUND_OPERATIONS_CREATE_FAIL") == "1" and any("repair_refund_operations" in arg for arg in args):
    raise SystemExit(1)
elif name == "docker" and args[-3:] == ["redis-cli", "INFO", "server"]:
    if os.environ.get("MOCK_REDIS_INFO_FAIL") == "1":
        raise SystemExit(1)
    sys.stdout.write(os.environ["MOCK_REDIS_INFO"])
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
                   "TEST_FINALIZATION_MODEL_MYSQL_DSN": "untrusted-ambient-fixture",
                   "TEST_FINALIZATION_MODEL_POSTGRES_DSN": "untrusted-ambient-fixture",
                   "TEST_FINALIZATION_SERVICE_MYSQL_DSN": "untrusted-ambient-fixture",
                   "TEST_FINALIZATION_SERVICE_POSTGRES_DSN": "untrusted-ambient-fixture",
                   "TEST_REFUND_OPS_MYSQL_DSN": "untrusted-ambient-fixture",
                   "TEST_REFUND_OPS_POSTGRES_DSN": "untrusted-ambient-fixture",
                   "TEST_REFUND_QUEUE_PRESSURE": "untrusted-ambient-fixture",
                   "TEST_FINALIZATION_CACHE_MYSQL_DSN": "untrusted-ambient-fixture",
                   "TEST_FINALIZATION_CACHE_POSTGRES_DSN": "untrusted-ambient-fixture",
                   "TEST_SETTLEMENT_DEADLINE_REDIS_ADDR": "untrusted-ambient-fixture",
                   "TEST_SETTLEMENT_DEADLINE_REDIS_DISPOSABLE": "untrusted-ambient-fixture",
                   "MOCK_REFUND_OPERATIONS_CREATE_FAIL": "1" if refund_operations_creation_fails else "0",
                   "MOCK_FINALIZATION_CREATE_FAIL": finalization_creation_fails,
                   "MOCK_TRACE": str(trace), "MOCK_REJECT_LABEL": reject_label,
                   "MOCK_CREATE_FAIL": "1" if creation_fails else "0",
                   "REDIS_ID": "d" * 64, "REDIS_PORT": "16379", "REDIS_EXPECTED_VERSION": redis_version,
                   "MOCK_REDIS_INFO": redis_info, "MOCK_REDIS_INFO_FAIL": "1" if redis_info_fails else "0"}
            result = subprocess.run(["bash", str(SCRIPT), mode], cwd=root, env=env,
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
                self.assertEqual(26, len(tests))
                self.assertEqual(len(tests), len(gates), "every suite must retain its strict evidence gate")
                self.assertEqual({"startup", "channel-priority", "audit-ordered-index", "setup-aff-code-index",
                                  "token-migration", "token-audit", "performance-aggregation", "performance-runtime-flush",
                                  "setup-wallet", "task-migration", "dashboard-migration", "midjourney-migration",
                                  "wallet", "owner", "owner-group-settings", "monitor-visibility", "sensitive-group-rename",
                                  "converted-responses", "native-responses", "immediate-settlement",
                                  "subscription-period", "subscription-period-migration",
                                  "request-finalization-model", "request-finalization-service", "refund-operations",
                                  "finalization-cache"},
                                 {Path(gate["args"][2]).stem for gate in gates})
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
        for label in ("token-migration", "token-audit", "subscription-period", "subscription-period-migration",
                      "request-finalization-model", "request-finalization-service", "refund-operations", "finalization-cache"):
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

    def test_finalization_matrices_use_distinct_fresh_databases_and_all_leaves(self):
        inventory = json.loads((SCRIPT.parent / "billing_finalization_required_cases.json").read_text())
        suites = {suite["package"]: suite for suite in inventory["suites"]}
        self.assertEqual({"model", "service"}, set(suites))
        self.assertEqual(28, len(suites["model"]["required_leaf_suffixes"]))
        self.assertEqual(77, len(suites["service"]["required_leaf_suffixes"]))
        self.assertEqual(["settlement_deadline_row_lock"], suites["service"]["native_required_leaf_suffixes"])
        for suffix in ("legacy_final_more_less/Å", "legacy_final_more_less/ĕ"):
            self.assertIn(suffix, suites["service"]["required_leaf_suffixes"])
        for dialect in ("sqlite", "mysql", "postgres"):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect)
                self.assertEqual(0, result.returncode, result.stderr)
                tests = [row for row in rows if row["tool"] == "go" and row["args"] != ["version"]]
                checks = [row for row in rows if row["tool"] == "python3"]
                self.assertEqual(58 if dialect == "sqlite" else 26, len(tests))
                self.assertEqual(len(tests), len(checks))
                for invocation, check in zip(tests, checks):
                    label = Path(check["args"][2]).stem
                    env = invocation["env"]
                    for package, suite in suites.items():
                        active = label == "request-finalization-" + package
                        for engine in ("mysql", "postgres"):
                            key = f"TEST_FINALIZATION_{package.upper()}_{engine.upper()}_DSN"
                            if active and dialect == engine:
                                self.assertIn("/repair_finalization_" + package + "?", env[key])
                                self.assertNotEqual(env["TEST_DATABASE_DSN"], env[key])
                                self.assertIn("127.0.0.1:", env[key])
                            else:
                                self.assertEqual("", env[key], (label, key))
                        if active:
                            prefix = suite["top"] + "/" + dialect
                            expected = [prefix] + [prefix + "/" + leaf for leaf in suite["required_leaf_suffixes"]]
                            if dialect != "sqlite":
                                expected += [prefix + "/" + leaf for leaf in suite.get("native_required_leaf_suffixes", [])]
                            self.assertEqual(expected, check["args"][3:])
                            self.assertEqual("./" + package, invocation["args"][4])
                            self.assertEqual(f"^{suite['top']}$/{dialect}$",
                                             invocation["args"][invocation["args"].index("-run") + 1])
                    if dialect != "sqlite":
                        for package in suites:
                            matching = [row for row in rows if row["tool"] == "docker" and
                                        any("repair_finalization_" + package in arg for arg in row["args"])]
                            self.assertEqual(1, len(matching))
                            command = " ".join(matching[0]["args"])
                            self.assertNotIn("IF NOT EXISTS", command)
                            self.assertNotIn("DROP", command)

    def test_finalization_database_creation_failure_stops_before_tests(self):
        for dialect in ("mysql", "postgres"):
            for package in ("model", "service", "cache"):
                with self.subTest(dialect=dialect, package=package):
                    result, rows = self.run_script(dialect, finalization_creation_fails=package)
                    self.assertNotEqual(0, result.returncode)
                    self.assertFalse(any(row["tool"] == "go" and row["args"] != ["version"] for row in rows))

    def test_refund_operations_isolates_ambient_env_and_requires_all_financial_leaves(self):
        inventory = json.loads((SCRIPT.parent / "billing_refund_followup_required_cases.json").read_text())
        suite = inventory["database_suite"]
        for dialect in ("sqlite", "mysql", "postgres"):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect)
                self.assertEqual(0, result.returncode, result.stderr)
                tests = [row for row in rows if row["tool"] == "go" and row["args"] != ["version"]]
                checks = [row for row in rows if row["tool"] == "python3"]
                self.assertEqual(58 if dialect == "sqlite" else 26, len(tests))
                for invocation, check in zip(tests, checks):
                    label = Path(check["args"][2]).stem
                    active = label == suite["label"]
                    env = invocation["env"]
                    self.assertEqual("1" if active else "", env["TEST_REFUND_QUEUE_PRESSURE"])
                    for engine, key in suite["dsn_env"].items():
                        if active and engine == dialect:
                            self.assertIn("/" + suite["database"] + "?", env[key])
                            self.assertIn("127.0.0.1:", env[key])
                            self.assertNotEqual(env["TEST_DATABASE_DSN"], env[key])
                            self.assertNotIn("repair_finalization_", env[key])
                        else:
                            self.assertEqual("", env[key], (label, key))
                    if not active:
                        continue
                    prefix = suite["top"] + "/" + dialect
                    expected = [suite["top"], prefix]
                    for mode in suite["modes"]:
                        expected.append(prefix + "/" + mode)
                        leaves = suite["common_mode_leaf_suffixes"][:]
                        if dialect != "sqlite":
                            leaves += suite["native_mode_leaf_suffixes"]
                        expected += [prefix + "/" + mode + "/" + leaf for leaf in leaves]
                    expected += [prefix + "/" + leaf for leaf in suite["engine_leaf_suffixes"]]
                    self.assertEqual(suite["expected_passing_test_events"][dialect], len(expected))
                    self.assertEqual(expected, check["args"][3:])
                    self.assertEqual("./model", invocation["args"][4])
                    self.assertEqual(f"^{suite['top']}$/{dialect}$",
                                     invocation["args"][invocation["args"].index("-run") + 1])
                creation = [row for row in rows if row["tool"] == "docker" and
                            any(suite["database"] in arg for arg in row["args"])]
                self.assertEqual(0 if dialect == "sqlite" else 1, len(creation))
                if creation:
                    command = " ".join(creation[0]["args"])
                    self.assertNotIn("IF NOT EXISTS", command)
                    self.assertNotIn("DROP", command)
                    self.assertLess(rows.index(creation[0]), rows.index(tests[0]))

    def test_refund_operations_create_failure_stops_before_any_tests(self):
        for dialect in ("mysql", "postgres"):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect, refund_operations_creation_fails=True)
                self.assertNotEqual(0, result.returncode)
                self.assertFalse(any(row["tool"] == "go" and row["args"] != ["version"] for row in rows))
                self.assertFalse(any(row["tool"] == "python3" for row in rows))

    def test_cache_matrix_has_fresh_separate_database_and_exact_required_leaves(self):
        inventory = json.loads((SCRIPT.parent / "billing_settlement_cache_required_cases.json").read_text())
        suite = inventory["database_suite"]
        stalled = inventory["sqlite_only_suite"]
        self.assertEqual(5, len(suite["required_leaf_suffixes"]))
        for dialect in ("sqlite", "mysql", "postgres"):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect)
                self.assertEqual(0, result.returncode, result.stderr)
                tests = [row for row in rows if row["tool"] == "go" and row["args"] != ["version"]]
                checks = [row for row in rows if row["tool"] == "python3"]
                seen = set()
                for invocation, check in zip(tests, checks):
                    label = Path(check["args"][2]).stem
                    env = invocation["env"]
                    active = label == suite["label"]
                    self.assertEqual("", env["TEST_SETTLEMENT_DEADLINE_REDIS_ADDR"])
                    self.assertEqual("", env["TEST_SETTLEMENT_DEADLINE_REDIS_DISPOSABLE"])
                    for engine, key in suite["dsn_env"].items():
                        if active and engine == dialect:
                            self.assertIn("/" + suite["database"] + "?", env[key])
                            self.assertIn("127.0.0.1:", env[key])
                            for other in ("repair_compat", "repair_finalization_model", "repair_finalization_service", "repair_refund_operations"):
                                self.assertNotIn("/" + other + "?", env[key])
                        else:
                            self.assertEqual("", env[key], (label, key))
                    if active:
                        seen.add(label)
                        prefix = suite["top"] + "/" + dialect
                        expected = [suite["top"], prefix] + [prefix + "/" + leaf for leaf in suite["required_leaf_suffixes"]]
                        self.assertEqual(suite["expected_passing_test_events"], len(expected))
                        self.assertEqual(expected, check["args"][3:])
                        self.assertEqual("./" + suite["package"], invocation["args"][4])
                        self.assertEqual(f"^{suite['top']}$/{dialect}$", invocation["args"][invocation["args"].index("-run") + 1])
                    if label == stalled["label"]:
                        seen.add(label)
                        self.assertEqual(stalled["required_tests"], check["args"][3:])
                        self.assertEqual("./" + stalled["package"], invocation["args"][4])
                        self.assertEqual(f"^{stalled['top']}$", invocation["args"][invocation["args"].index("-run") + 1])
                expected_seen = {suite["label"]}
                if dialect == "sqlite":
                    expected_seen.add(stalled["label"])
                self.assertEqual(expected_seen, seen)
                creation = [row for row in rows if row["tool"] == "docker" and any(suite["database"] in arg for arg in row["args"])]
                self.assertEqual(0 if dialect == "sqlite" else 1, len(creation))
                if creation:
                    self.assertLess(rows.index(creation[0]), rows.index(tests[0]))
                    self.assertNotIn("IF NOT EXISTS", " ".join(creation[0]["args"]))
                    self.assertNotIn("DROP", " ".join(creation[0]["args"]))

    def test_sqlite_only_followups_require_exact_nonhelper_intent_presenter_and_deadline_tests(self):
        inventory = json.loads((SCRIPT.parent / "billing_refund_followup_required_cases.json").read_text())
        suites = {suite["label"]: suite for suite in inventory["sqlite_only_suites"]}
        self.assertEqual({"refund-intent-process", "task-persistence-presenter", "refund-deadline-classifier"}, set(suites))
        self.assertEqual([14, 11, 16], [suite["expected_passing_test_events"] for suite in suites.values()])
        for dialect in ("sqlite", "mysql", "postgres"):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect)
                self.assertEqual(0, result.returncode, result.stderr)
                tests = [row for row in rows if row["tool"] == "go" and row["args"] != ["version"]]
                checks = [row for row in rows if row["tool"] == "python3"]
                selected = set()
                for invocation, check in zip(tests, checks):
                    label = Path(check["args"][2]).stem
                    if label not in suites:
                        continue
                    selected.add(label)
                    suite = suites[label]
                    self.assertEqual(suite["required_tests"], check["args"][3:])
                    self.assertEqual(suite["expected_passing_test_events"], len(check["args"][3:]))
                    self.assertEqual("./" + suite["package"], invocation["args"][4])
                    selector = invocation["args"][invocation["args"].index("-run") + 1]
                    self.assertEqual(suite["selector"], selector)
                    tops = [test for test in suite["required_tests"] if "/" not in test]
                    for top in tops:
                        self.assertIsNotNone(re.fullmatch(selector, top))
                    self.assertIsNone(re.fullmatch(selector, "TestBillingRefundIntentProcessExitHelper"))
                    self.assertEqual("", invocation["env"]["TEST_REFUND_QUEUE_PRESSURE"])
                self.assertEqual(set(suites) if dialect == "sqlite" else set(), selected)

    def test_followup_required_rejections_fail_but_retain_later_independent_reports(self):
        for label in ("refund-operations", "refund-intent-process", "task-persistence-presenter", "refund-deadline-classifier",
                      "finalization-cache", "finalization-cache-stalled-redis"):
            with self.subTest(label=label):
                result, rows = self.run_script("sqlite", reject_label=label)
                self.assertNotEqual(0, result.returncode)
                self.assertIn("Required repair contracts failed", result.stderr)
                checks = [row for row in rows if row["tool"] == "python3"]
                self.assertEqual(58, len(checks))
                self.assertEqual("sensitive-task-selected-plugin", Path(checks[-1]["args"][2]).stem)

    def test_actual_strict_checker_rejects_each_missing_followup_required_path(self):
        spec = importlib.util.spec_from_file_location("refund_followup_report_checker", SCRIPT.parent / "check_go_test_report.py")
        checker = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(checker)
        # Shell stubs only inspect orchestration. Feed synthetic evidence to the
        # actual unchanged checker so a missing required financial leaf is fatal.
        for dialect in ("sqlite", "mysql", "postgres"):
            result, rows = self.run_script(dialect)
            self.assertEqual(0, result.returncode, result.stderr)
            for row in rows:
                if row["tool"] != "python3":
                    continue
                label = Path(row["args"][2]).stem
                if label not in {"request-finalization-service", "refund-operations", "refund-intent-process",
                                 "task-persistence-presenter", "refund-deadline-classifier",
                                 "finalization-cache", "finalization-cache-stalled-redis"}:
                    continue
                required = row["args"][3:]
                with tempfile.TemporaryDirectory() as directory:
                    report = Path(directory) / "synthetic.jsonl"
                    events = [{"Action": "pass", "Package": "offline/fixture", "Test": test} for test in required]
                    events.append({"Action": "pass", "Package": "offline/fixture"})
                    report.write_text("".join(json.dumps(event) + "\n" for event in events))
                    self.assertEqual(len(required), len(checker.validate_report(report, required, True)))
                    for missing in required:
                        with self.subTest(dialect=dialect, label=label, missing=missing):
                            report.write_text("".join(json.dumps(event) + "\n" for event in events if event.get("Test") != missing))
                            with self.assertRaisesRegex(ValueError, "required test did not pass"):
                                checker.validate_report(report, required, True)
                    report.write_text("".join(json.dumps(event) + "\n" for event in events + [
                        {"Action": "skip", "Package": "offline/fixture", "Test": required[0] + "/unexpected_skipped_contract"}]))
                    with self.assertRaisesRegex(ValueError, "descendant was skipped"):
                        checker.validate_report(report, required, True)

    def test_subscription_period_required_cases_and_selectors_are_complete(self):
        for dialect in ("sqlite", "mysql", "postgres"):
            with self.subTest(dialect=dialect):
                result, rows = self.run_script(dialect)
                self.assertEqual(0, result.returncode, result.stderr)
                tests = [row for row in rows if row["tool"] == "go" and row["args"] != ["version"]]
                checks = [row for row in rows if row["tool"] == "python3"]
                gates = {Path(row["args"][2]).stem: row["args"][3:] for row in checks}
                expected_cases = (
                    "same_period_settle", "same_period_upward", "same_period_zero", "same_period_refund",
                    "quota_ceiling", "journal_identity", "preconsume_replay", "parallel_preconsume",
                    "auto_reset_settle", "auto_reset_refund", "manual_reset_settle", "same_second_reset_refund",
                    "old_positive_settle", "reserve_closed_period", "legacy_unknown", "deleted_source",
                    "expired_source", "underflow", "parallel_settle", "parallel_refund", "reset_races_refund")
                prefix = f"TestSubscriptionPeriodAccountingDatabaseMatrix/{dialect}"
                self.assertEqual([prefix] + [prefix + "/" + case for case in expected_cases], gates["subscription-period"])
                prefix = f"TestSubscriptionPeriodMigrationDatabaseMatrix/{dialect}"
                self.assertEqual([prefix + "/fresh", prefix + "/upgrade"], gates["subscription-period-migration"])
                for case in ("late_submit", "late_terminal_refund", "late_terminal_positive",
                             "unknown_legacy_task", "settled_source_pending_task",
                             "live_session_refund", "live_session_reset_refund", "wallet_session_clears_period"):
                    self.assertIn(f"TestTaskWalletReceiptsDatabaseMatrix/{dialect}/subscription_period_{case}", gates["wallet"])
                for test, gate in zip(tests, checks):
                    label = Path(gate["args"][2]).stem
                    if label in ("subscription-period", "subscription-period-migration"):
                        test_name = "TestSubscriptionPeriod" + ("Accounting" if label == "subscription-period" else "Migration") + "DatabaseMatrix"
                        self.assertEqual("./model", test["args"][4])
                        self.assertEqual(f"^{test_name}$/{dialect}$", test["args"][test["args"].index("-run") + 1])
                if dialect == "sqlite":
                    self.assertEqual(["TestSubscriptionPeriodTransactionRollback",
                                      "TestSubscriptionPeriodReplayRejectsStaleConsumedObservation",
                                      "TestSubscriptionPeriodJournalReserveSettlementValidation",
                                      "TestSubscriptionPeriodReplayAfterAbsentSnapshot",
                                      "TestSubscriptionRefundCommitsReceiptAndUsageTogether"], gates["subscription-period-replay"])
                else:
                    self.assertNotIn("subscription-period-replay", gates)

    def test_both_pinned_redis_versions_keep_all_real_contracts_and_loopback_dsns(self):
        expected = {
            "model-script-flush": ["TestModelRateLimitRealRedis/reservation_replay_and_expiry",
                                   "TestModelRateLimitRealRedis/script_flush_resumes_request_pipeline"],
            "token-bucket-script-flush": ["TestAllowFallsBackToEvalAfterScriptFlush/real_redis"],
            "task-token-reservation": ["TestTaskBillingBatchReservationRestartRecovery/limited_with_redis",
                                       "TestTaskBillingBatchReservationRestartRecovery/unlimited_with_redis",
                                       "TestTaskBillingBatchReservationRestartRecovery/limited_with_ordinary_request"],
            "settlement-deadline-redis": ["TestBillingSettlementDeadlineNativeRedisFence"],
        }
        for version in ("7.2.16", "8.8.0"):
            with self.subTest(version=version):
                result, rows = self.run_script("", mode="redis", redis_version=version,
                                               redis_info=f"# Server\r\nredis_version:{version}\r\nredis_mode:standalone\r\n")
                self.assertEqual(0, result.returncode, result.stderr)
                tests = [row for row in rows if row["tool"] == "go" and row["args"] != ["version"]]
                gates = [row for row in rows if row["tool"] == "python3"]
                self.assertEqual(4, len(tests))
                self.assertEqual(4, len(gates))
                self.assertEqual(set(expected), {Path(row["args"][2]).stem for row in gates})
                for test, gate in zip(tests, gates):
                    label = Path(gate["args"][2]).stem
                    self.assertEqual(expected[label], gate["args"][3:])
                    self.assertEqual("1", test["env"]["TEST_RUNTIME_REDIS"])
                    self.assertEqual("", test["env"]["TEST_REFUND_OPS_MYSQL_DSN"])
                    self.assertEqual("", test["env"]["TEST_REFUND_OPS_POSTGRES_DSN"])
                    self.assertEqual("", test["env"]["TEST_REFUND_QUEUE_PRESSURE"])
                    self.assertEqual("", test["env"]["TEST_FINALIZATION_CACHE_MYSQL_DSN"])
                    self.assertEqual("", test["env"]["TEST_FINALIZATION_CACHE_POSTGRES_DSN"])
                    self.assertEqual("127.0.0.1:16379" if label == "settlement-deadline-redis" else "",
                                     test["env"]["TEST_SETTLEMENT_DEADLINE_REDIS_ADDR"])
                    self.assertEqual("1" if label == "settlement-deadline-redis" else "",
                                     test["env"]["TEST_SETTLEMENT_DEADLINE_REDIS_DISPOSABLE"])
                    self.assertEqual("redis://127.0.0.1:16379/0", test["env"]["TEST_REDIS_DSN"])
                    self.assertEqual("127.0.0.1:16379", test["env"]["TEST_REDIS_ADDR"])
                    if label == "task-token-reservation":
                        self.assertEqual("127.0.0.1:16379", test["env"]["TEST_TASK_REDIS_ADDR"])

    def test_wrong_missing_ambiguous_and_malformed_redis_versions_fail_before_tests(self):
        cases = [("8.8.0", "redis_version:7.2.16\r\n"), ("8.8.0", ""),
                 ("8.8.0", "redis_version:8.8.0\nredis_version:8.8.0\n"),
                 ("8.8.0", "redis_version:8.8.0\nredis_version:\n"),
                 ("8.8.0", "redis_version:8.8.0:extra\n"), ("8.8.0", "redis_version:8.8.0-rc1\n"),
                 ("8.8.0", " redis_version:8.8.0\n"), ("", "redis_version:8.8.0\n"),
                 ("8.8", "redis_version:8.8\n")]
        for expected, info in cases:
            with self.subTest(expected=expected, info=info):
                result, rows = self.run_script("", mode="redis", redis_version=expected, redis_info=info)
                self.assertNotEqual(0, result.returncode)
                self.assertFalse(any(row["tool"] == "go" and row["args"] != ["version"] for row in rows))

    def test_native_redis_deadline_inventory_and_actual_strict_checker(self):
        suite = json.loads((SCRIPT.parent / "billing_settlement_cache_required_cases.json").read_text())["redis_suite"]
        result, rows = self.run_script("", mode="redis")
        self.assertEqual(0, result.returncode, result.stderr)
        tests = [row for row in rows if row["tool"] == "go" and row["args"] != ["version"]]
        checks = [row for row in rows if row["tool"] == "python3"]
        matches = [(test, check) for test, check in zip(tests, checks) if Path(check["args"][2]).stem == suite["label"]]
        self.assertEqual(1, len(matches))
        test, check = matches[0]
        self.assertEqual(suite["required_tests"], check["args"][3:])
        self.assertEqual("./" + suite["package"], test["args"][4])
        self.assertEqual(f"^{suite['top']}$", test["args"][test["args"].index("-run") + 1])
        self.assertEqual("127.0.0.1:16379", test["env"][suite["address_env"]])
        self.assertEqual("1", test["env"][suite["disposable_env"]])
        spec = importlib.util.spec_from_file_location("settlement_redis_report_checker", SCRIPT.parent / "check_go_test_report.py")
        checker = importlib.util.module_from_spec(spec)
        spec.loader.exec_module(checker)
        with tempfile.TemporaryDirectory() as directory:
            report = Path(directory) / "synthetic.jsonl"
            events = [{"Action": "pass", "Package": "offline/fixture", "Test": suite["top"]},
                      {"Action": "pass", "Package": "offline/fixture"}]
            report.write_text("".join(json.dumps(event) + "\n" for event in events))
            self.assertEqual(1, len(checker.validate_report(report, suite["required_tests"], True)))
            report.write_text(json.dumps(events[1]) + "\n")
            with self.assertRaisesRegex(ValueError, "required test did not pass"):
                checker.validate_report(report, suite["required_tests"], True)
            events[0]["Action"] = "skip"
            report.write_text("".join(json.dumps(event) + "\n" for event in events))
            with self.assertRaises(ValueError):
                checker.validate_report(report, suite["required_tests"], True)

    def test_failed_redis_info_query_cannot_start_contracts(self):
        result, rows = self.run_script("", mode="redis", redis_info_fails=True)
        self.assertNotEqual(0, result.returncode)
        self.assertFalse(any(row["tool"] == "go" and row["args"] != ["version"] for row in rows))

    def test_redis_required_rejection_keeps_later_evidence_and_fails_job(self):
        for label in ("model-script-flush", "token-bucket-script-flush", "task-token-reservation", "settlement-deadline-redis"):
            with self.subTest(label=label):
                result, rows = self.run_script("", mode="redis", reject_label=label)
                self.assertNotEqual(0, result.returncode)
                self.assertIn("Required repair contracts failed", result.stderr)
                self.assertEqual(4, sum(row["tool"] == "python3" for row in rows))


if __name__ == "__main__":
    unittest.main()
