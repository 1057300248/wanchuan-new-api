import copy
import importlib.util
import json
from pathlib import Path
import subprocess
import tempfile
import unittest
from unittest.mock import patch


spec = importlib.util.spec_from_file_location("lifecycle", Path(__file__).parents[1] / "verify_application_lifecycle.py")
lifecycle = importlib.util.module_from_spec(spec)
spec.loader.exec_module(lifecycle)


def snapshot_fixture():
    fixture = {}
    for database, tables in (("main", lifecycle.MAIN_ROWS), ("log", lifecycle.LOG_ROWS)):
        fixture[database] = {table: [{"id": 1, "value": "preserved"}] for table in tables}
        fixture[database]["columns"] = {"fixture.id": {"type": "int8", "nullable": "NO"}}
        fixture[database]["indexes"] = {"fixture_unique": "CREATE UNIQUE INDEX fixture_unique ON fixture (id)"}
        fixture[database]["constraints"] = {"fixture.fixture_pkey": "PRIMARY KEY (id)"}
    fixture["main"]["options"] = [
        {"key": "ChuanapiMonitorConfig", "value": json.dumps(lifecycle.MONITOR_CONFIG)},
        {"key": "DataExportEnabled", "value": "false"},
        {"key": "ModelRatio", "value": '{"synthetic-upgrade-model":2.5}'},
        {"key": "PublicModelOwnedBy", "value": "synthetic-owner-label"},
        {"key": "TaskEnabled", "value": "false"},
    ]
    fixture["main"]["users"] = [{"id": 1, "quota": 5000, "quota_paid": 4000, "quota_bonus": 1000, "used_quota": 4294967400}]
    fixture["main"]["tokens"] = [{"id": 1, "remain_quota": 700, "used_quota": 300, "accessed_time": 0}]
    fixture["log"]["audit_logs"] = [{"id": 1, "other": {"enabled": False, "limit": 0, "large_id": 9007199254740993}}]
    return fixture


class ApplicationLifecycleEvidenceTests(unittest.TestCase):
    def test_fixture_token_has_no_channel_selector_suffix(self):
        # TokenAuth strips sk- and splits channel selectors on '-'. A malformed
        # fixture would fail authentication before testing version compatibility.
        credential = ("sk-" + lifecycle.TOKEN).removeprefix("sk-").split("-")[0]
        self.assertEqual(lifecycle.TOKEN, credential)
        self.assertRegex(credential, r"^[A-Za-z0-9]{48}$")

    def test_additive_upgrade_and_request_timestamp_preserve_old_rows(self):
        old = snapshot_fixture()
        new = copy.deepcopy(old)
        new["main"]["tasks"][0]["settlement_pending"] = False
        new["main"]["quota_data"][0]["cache_read_tokens"] = 0
        new["main"]["tokens"][0]["accessed_time"] = 1790000000
        new["main"]["options"][0]["value"] = json.dumps(lifecycle.MONITOR_CONFIG, sort_keys=True, separators=(",", ":"))
        lifecycle.check_snapshot(old, new)

    def test_wallet_identity_and_log_corruption_fail_closed(self):
        cases = (("main", "users", "quota", 4999), ("main", "tokens", "remain_quota", 699),
                 ("main", "models", "value", "lost"), ("log", "logs", "value", "lost"),
                 ("log", "audit_logs", "other", {"enabled": False, "limit": 0, "large_id": 9007199254740992}))
        for database, table, column, changed in cases:
            with self.subTest(table=table, column=column):
                old = snapshot_fixture()
                new = copy.deepcopy(old)
                new[database][table][0][column] = changed
                with self.assertRaises(RuntimeError):
                    lifecycle.check_snapshot(old, new)

    def test_clearing_hidden_groups_is_not_accepted_as_preservation(self):
        old = snapshot_fixture()
        new = copy.deepcopy(old)
        changed = copy.deepcopy(lifecycle.MONITOR_CONFIG)
        changed["hidden_groups"] = {}
        new["main"]["options"][0]["value"] = json.dumps(changed)
        with self.assertRaisesRegex(RuntimeError, "ChuanapiMonitorConfig"):
            lifecycle.check_snapshot(old, new)

    def test_missing_empty_or_duplicate_fixtures_cannot_pass(self):
        for database, table in (("main", "users"), ("main", "options"), ("log", "audit_logs")):
            for count in (0, 2):
                with self.subTest(database=database, table=table, count=count):
                    old = snapshot_fixture()
                    old[database][table] = old[database][table][:1] * count
                    with self.assertRaises(RuntimeError):
                        lifecycle.check_snapshot(old, old)
        with self.assertRaises(RuntimeError):
            lifecycle.check_snapshot({"main": {}}, {"main": {}})

    def test_index_or_constraint_loss_cannot_pass(self):
        for database in ("main", "log"):
            for kind in ("columns", "indexes", "constraints"):
                with self.subTest(database=database, kind=kind):
                    old = snapshot_fixture()
                    new = copy.deepcopy(old)
                    new[database][kind] = {}
                    with self.assertRaises(RuntimeError):
                        lifecycle.check_snapshot(old, new)
                    with self.assertRaises(RuntimeError):
                        lifecycle.check_snapshot(new, new)

    def test_restart_must_preserve_candidate_schema_not_only_baseline(self):
        baseline = snapshot_fixture()
        upgraded = copy.deepcopy(baseline)
        upgraded["main"]["columns"]["tasks.settlement_pending"] = {"type": "bool", "nullable": "YES"}
        upgraded["main"]["indexes"]["idx_tasks_settlement_pending"] = "CREATE INDEX idx_tasks_settlement_pending ON tasks (settlement_pending)"
        lifecycle.check_snapshot(baseline, upgraded)
        lifecycle.check_snapshot(upgraded, upgraded, same_schema=True)
        with self.assertRaises(RuntimeError):
            lifecycle.check_snapshot(upgraded, baseline, same_schema=True)
        repeated = copy.deepcopy(upgraded)
        repeated["main"]["columns"]["tasks.unexpected"] = {"type": "int8", "nullable": "YES"}
        with self.assertRaises(RuntimeError):
            lifecycle.check_snapshot(upgraded, repeated, same_schema=True)

    def test_model_visibility_and_candidate_owner_contract(self):
        baseline = {"object": "list", "data": [{"id": lifecycle.MODEL, "owned_by": "baseline-provider"}]}
        lifecycle.check_models(baseline, candidate=False)
        with self.assertRaises(RuntimeError):
            lifecycle.check_models(baseline, candidate=True)
        candidate = copy.deepcopy(baseline)
        candidate["data"][0]["owned_by"] = "synthetic-owner-label"
        lifecycle.check_models(candidate, candidate=True)
        for body in ({"success": False}, {"object": "list", "data": []},
                     {"object": "list", "data": candidate["data"] + [{"id": "unrelated-private-model"}]}):
            with self.subTest(body=body), self.assertRaises(RuntimeError):
                lifecycle.check_models(body, candidate=True)

    def test_provenance_rejects_short_wrong_and_dirty_source(self):
        with tempfile.TemporaryDirectory() as directory:
            path = Path(directory)
            (path / "VERSION").write_text("synthetic-version\n")
            with self.assertRaises(RuntimeError):
                lifecycle.verify_checkout(path, "f25c536764")
            with patch.object(subprocess, "check_output", return_value="0" * 40 + "\n"):
                with self.assertRaises(RuntimeError):
                    lifecycle.verify_checkout(path, lifecycle.BASELINE_SHA)
            with patch.object(subprocess, "check_output", side_effect=[lifecycle.BASELINE_SHA + "\n", " M go.mod\n"]):
                with self.assertRaises(RuntimeError):
                    lifecycle.verify_checkout(path, lifecycle.BASELINE_SHA)
            with patch.object(subprocess, "check_output", side_effect=[lifecycle.BASELINE_SHA + "\n", "", ".env\n"]):
                with self.assertRaises(RuntimeError):
                    lifecycle.verify_checkout(path, lifecycle.BASELINE_SHA)
            with patch.object(subprocess, "check_output", side_effect=[lifecycle.BASELINE_SHA + "\n", "", ""]):
                self.assertEqual("synthetic-version", lifecycle.verify_checkout(path, lifecycle.BASELINE_SHA))

    def test_remote_docker_environment_is_never_used(self):
        with tempfile.TemporaryDirectory() as directory, patch.dict("os.environ", {"DOCKER_HOST": "tcp://production.invalid:2375", "DOCKER_CONTEXT": "production"}):
            runner = lifecycle.Lifecycle(Path(directory))
            with patch.object(subprocess, "run", return_value=subprocess.CompletedProcess([], 0, "ok", "")) as execute:
                runner.docker("version")
            arguments, options = execute.call_args
            self.assertEqual(["docker", "--host", "unix:///var/run/docker.sock", "version"], arguments[0])
            self.assertNotIn("DOCKER_HOST", options["env"])
            self.assertNotIn("DOCKER_CONTEXT", options["env"])


if __name__ == "__main__":
    unittest.main()
