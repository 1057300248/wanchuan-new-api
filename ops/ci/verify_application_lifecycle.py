#!/usr/bin/env python3
"""Synthetic, source-built application upgrade/restart/rollback on disposable PG.

This is NOT the deployed production image, its extra patches, or representative
production data. No external DSN, volume, image override, or credentials are
accepted. Only a local Docker daemon is used; runtime has an internal network,
no published ports, and no upstream requests. Both apps use the candidate's
Dockerfile.api (the baseline recipe omits tokenkit/go.mod before go mod download).
"""

import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import subprocess
import time
import uuid


BASELINE_SHA = "f25c5367641ba6446145e375a36cc84ff52107ed"
POSTGRES_IMAGE = "postgres:18.6-alpine@sha256:d3e1620b530c944afa6e887d22eb899824da68e19c52024bf98f5220c88a65b2"
MODEL = "synthetic-upgrade-model"
TOKEN = "synthetic" + "x" * 39  # No '-' channel-selector suffix in an API key.
PASSWORD = "Synthetic-Only-Upgrade-2026!"
MONITOR_CONFIG = {"enabled": False, "title": "Synthetic monitor", "description": "Synthetic historical settings",
                  "groups": {"route-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": "Private fixture"},
                  "hidden_groups": {"route-aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa": True}}

MAIN_SEED = f"""
BEGIN;
INSERT INTO users (username,password,aff_code,role,status,\"group\",quota,quota_paid,quota_bonus,quota_reward,used_quota,auth_version)
VALUES ('synthetic-owner','not-a-login-password-hash','synthetic-upgrade-aff',1,1,'default',5000,4000,1000,0,4294967400,7);
INSERT INTO tokens (user_id,\"key\",name,status,expired_time,remain_quota,used_quota,\"group\",model_limits_enabled,model_limits,unlimited_quota)
SELECT id,'{TOKEN}','synthetic-token',1,-1,700,300,'default',true,'{MODEL}',false FROM users WHERE username='synthetic-owner';
INSERT INTO channels (name,type,\"key\",status,base_url,models,\"group\",channel_info)
VALUES ('synthetic-channel',1,'not-an-upstream-credential',1,'http://127.0.0.1:9','{MODEL}','default','{{}}');
INSERT INTO abilities (\"group\",model,channel_id,enabled)
SELECT 'default','{MODEL}',id,true FROM channels WHERE name='synthetic-channel';
INSERT INTO models (model_name,status,sync_official,context_length,capabilities,bonus_eligible)
VALUES ('{MODEL}',1,0,128000,'["vision"]',false);
INSERT INTO tasks (task_id,platform,user_id,\"group\",quota,status,progress,submit_time,finish_time,properties,private_data,data)
SELECT 'synthetic-completed-task','synthetic',id,'default',123,'SUCCESS','100%',1788600000,1788600001,'{{}}','{{}}','{{"synthetic":true}}'
FROM users WHERE username='synthetic-owner';
INSERT INTO quota_data (user_id,username,model_name,created_at,use_group,token_used,count,quota)
SELECT id,username,'{MODEL}',1788600000,'default',321,2,123 FROM users WHERE username='synthetic-owner';
INSERT INTO options (\"key\",value) VALUES
('ModelRatio','{{"{MODEL}":2.5}}'),
('ChuanapiMonitorConfig','{json.dumps(MONITOR_CONFIG)}'),
('monitor_setting.auto_test_channel_enabled','false'),
('PublicModelOwnedBy','synthetic-owner-label'),
('TaskEnabled','false'),('DataExportEnabled','false'),('Notice','synthetic-seed')
ON CONFLICT (\"key\") DO UPDATE SET value=EXCLUDED.value;
COMMIT;
"""

LOG_SEED = """
BEGIN;
INSERT INTO logs (user_id,created_at,type,content,quota,request_id,other)
VALUES (0,1788600000,2,'synthetic historical consume log',123,'synthetic-upgrade-log','{"cache_tokens":19}');
INSERT INTO audit_logs (event_id,user_id,username,actor_role,created_at,category,request_id,content,success,other)
VALUES ('synthetic-upgrade-event',0,'synthetic-owner',100,1788600000,'operation','synthetic-upgrade-audit',
'synthetic historical audit',false,'{"op":{"action":"model.update","params":{"enabled":false,"limit":0,"large_id":9007199254740993}}}');
COMMIT;
"""

MAIN_ROWS = {
    "users": "username='synthetic-owner'",
    "tokens": "name='synthetic-token'",
    "channels": "name='synthetic-channel'",
    "abilities": f"model='{MODEL}'",
    "models": f"model_name='{MODEL}'",
    "tasks": "task_id='synthetic-completed-task'",
    "quota_data": f"model_name='{MODEL}'",
    "options": "\"key\" IN ('ModelRatio','ChuanapiMonitorConfig','PublicModelOwnedBy','TaskEnabled','DataExportEnabled')",
}
LOG_ROWS = {"logs": "request_id='synthetic-upgrade-log'", "audit_logs": "event_id='synthetic-upgrade-event'"}


def require(condition, message):
    if not condition:
        raise RuntimeError(message)


def check_snapshot(before, after, same_schema=False):
    """Existing values and constraints must survive; additive columns may appear."""
    require(set(before) == {"main", "log"} == set(after), "missing primary or separate log snapshot")
    for database, rows in (("main", MAIN_ROWS), ("log", LOG_ROWS)):
        for table in rows:
            old = before[database][table]
            new = after[database][table]
            count = 5 if table == "options" else 1
            require(len(old) == len(new) == count, f"{database}.{table}: fixture count changed or missing")
            for previous, current in zip(old, new):
                for column, value in previous.items():
                    # A successful read-only API-token request may touch this timestamp.
                    if table == "tokens" and column == "accessed_time":
                        continue
                    if table == "options" and column == "value" and previous["key"] in {"ModelRatio", "ChuanapiMonitorConfig"}:
                        require(column in current and json.loads(current[column]) == json.loads(value),
                                f"{database}.{table}.{previous['key']}: saved JSON changed")
                        continue
                    require(column in current and current[column] == value,
                            f"{database}.{table}.{column}: pre-existing fixture value changed")
        for kind in ("columns", "indexes", "constraints"):
            old = before[database][kind]
            new = after[database][kind]
            require(bool(old), f"{database}: no {kind} evidence")
            for name, definition in old.items():
                require(new.get(name) == definition, f"{database}: {kind} {name} changed or disappeared")
            if same_schema:
                require(old == new, f"{database}: {kind} changed on repeated candidate startup")


def check_models(body, candidate):
    require(body.get("object") == "list" and isinstance(body.get("data"), list), "model list contract failed")
    require([row.get("id") for row in body["data"]] == [MODEL], "synthetic token model visibility changed")
    if candidate:
        require(body["data"][0].get("owned_by") == "synthetic-owner-label", "persisted candidate public owner was not loaded")


def verify_checkout(path, sha):
    require(re.fullmatch(r"[0-9a-f]{40}", sha) is not None, "source SHA must be immutable and complete")
    actual = subprocess.check_output(["git", "-C", str(path), "rev-parse", "HEAD"], text=True).strip()
    require(actual == sha, f"unexpected checkout revision: {path}")
    status = subprocess.check_output(["git", "-C", str(path), "status", "--porcelain", "--untracked-files=all"], text=True)
    require(not status, f"source checkout must be clean: {path}")
    ignored = subprocess.check_output(["git", "-C", str(path), "ls-files", "--others", "--ignored", "--exclude-standard"], text=True)
    require(not ignored, f"source checkout must not contain ignored local files or credentials: {path}")
    return (path / "VERSION").read_text().strip()


class Lifecycle:
    def __init__(self, out):
        self.out = out
        self.prefix = "new-api-synthetic-" + uuid.uuid4().hex[:12]
        self.network = self.prefix + "-net"
        self.pg = self.prefix + "-pg"
        self.app = None
        self.containers = []
        self.images = []
        self.env = os.environ.copy()
        self.env.pop("DOCKER_CONTEXT", None)
        self.env.pop("DOCKER_HOST", None)

    def docker(self, *args, input=None, check=True, timeout=180, evidence=None):
        command = ["docker", "--host", "unix:///var/run/docker.sock", *args]
        if evidence and args[0] in {"build", "pull", "logs", "version"}:
            # Stream build/diagnostic output directly to disk, retaining partial
            # evidence even when a subprocess or the job reaches its deadline.
            with (self.out / evidence).open("w") as output:
                result = subprocess.run(command, input=input, text=True, stdout=output,
                                        stderr=subprocess.STDOUT, timeout=timeout, env=self.env)
            result.stdout, result.stderr = (self.out / evidence).read_text(), ""
        else:
            result = subprocess.run(command, input=input, text=True, capture_output=True,
                                    timeout=timeout, env=self.env)
            if evidence:
                (self.out / evidence).write_text(result.stdout + result.stderr)
        require(not check or result.returncode == 0, f"docker {args[0]} failed; see retained evidence" if evidence else f"docker {args[0]} failed: {result.stderr}")
        return result

    def inspect(self, kind, name, evidence=None):
        return json.loads(self.docker(kind, "inspect", name, evidence=evidence).stdout)[0]

    def sql(self, database, query):
        return self.docker("exec", "-i", self.pg, "psql", "-X", "-qAt", "-v", "ON_ERROR_STOP=1", "-U", "postgres", "-d", database, input=query).stdout.strip()

    def snapshot(self, phase):
        snapshot = {}
        for key, database, tables in (("main", "synthetic_main", MAIN_ROWS), ("log", "synthetic_log", LOG_ROWS)):
            result = {}
            for table, predicate in tables.items():
                order = '"key"' if table == "options" else "1"
                raw = self.sql(database, f"SELECT COALESCE(jsonb_agg(to_jsonb(t)), '[]') FROM (SELECT * FROM {table} WHERE {predicate} ORDER BY {order}) t;")
                result[table] = json.loads(raw)
            names = ",".join("'" + table + "'" for table in tables)
            result["columns"] = json.loads(self.sql(database, f"SELECT COALESCE(jsonb_object_agg(table_name||'.'||column_name,jsonb_build_object('type',udt_name,'nullable',is_nullable,'default',column_default,'length',character_maximum_length,'precision',numeric_precision,'scale',numeric_scale)),'{{}}') FROM information_schema.columns WHERE table_schema='public' AND table_name IN ({names});"))
            result["indexes"] = json.loads(self.sql(database, f"SELECT COALESCE(jsonb_object_agg(indexname,indexdef),'{{}}') FROM pg_indexes WHERE schemaname='public' AND tablename IN ({names});"))
            result["constraints"] = json.loads(self.sql(database, f"SELECT COALESCE(jsonb_object_agg(c.relname||'.'||con.conname,pg_get_constraintdef(con.oid)),'{{}}') FROM pg_constraint con JOIN pg_class c ON c.oid=con.conrelid JOIN pg_namespace n ON n.oid=c.relnamespace WHERE n.nspname='public' AND c.relname IN ({names});"))
            snapshot[key] = result
        (self.out / f"{phase}-snapshot.json").write_text(json.dumps(snapshot, indent=2, sort_keys=True) + "\n")
        return snapshot

    def request(self, path, method="GET", payload=None, bearer=None):
        args = ["exec", self.app, "wget", "--quiet", "--timeout=5", "--tries=1", "--max-redirect=0", "--output-document=-", "--method=" + method]
        if payload is not None:
            args += ["--header=Content-Type: application/json", "--body-data=" + json.dumps(payload)]
        if bearer:
            args += ["--header=Authorization: Bearer " + bearer]
        response = self.docker(*args, "http://127.0.0.1:3000" + path, check=False)
        require(response.returncode == 0, f"{method} {path} failed inside the isolated application")
        return json.loads(response.stdout)

    def start(self, image, phase, version):
        self.app = self.prefix + "-" + phase
        self.containers.append(self.app)
        self.docker("run", "--detach", "--pull=never", "--name", self.app, "--network", self.network,
                    "--cap-drop=ALL", "--security-opt=no-new-privileges", "--tmpfs", "/tmp", "--tmpfs", "/data",
                    "--env", f"SQL_DSN=postgres://postgres:synthetic@{self.pg}:5432/synthetic_main?sslmode=disable",
                    "--env", f"LOG_SQL_DSN=postgres://postgres:synthetic@{self.pg}:5432/synthetic_log?sslmode=disable",
                    "--env", "REDIS_CONN_STRING=", "--env", "SESSION_SECRET=synthetic-upgrade-only-session-secret",
                    "--env", "CRYPTO_SECRET=synthetic-upgrade-only-crypto-secret", "--env", "DEFAULT_LANGUAGE=en",
                    "--env", "CHANNEL_TEST_ENABLED=false", "--env", "UPDATE_TASK=false", "--env", "MODEL_MONITOR_ENABLED=false", "--env", "GIN_MODE=release", image)
        state = self.inspect("container", self.app, evidence=f"{phase}-container.json")
        require(state["Image"] == image, "running app image ID does not match the built source image")
        require(not state["HostConfig"]["PortBindings"], "runtime must not publish host ports")
        require(set(state["NetworkSettings"]["Networks"]) == {self.network}, "runtime attached to unexpected network")
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            require(self.inspect("container", self.app)["State"]["Running"], f"{phase} exited during startup")
            probe = self.docker("exec", self.app, "wget", "--quiet", "--timeout=3", "--tries=1", "--output-document=-", "http://127.0.0.1:3000/api/readyz", check=False)
            if probe.returncode == 0:
                require(json.loads(probe.stdout) == {"success": True}, "invalid readiness response")
                break
            time.sleep(1)
        else:
            raise RuntimeError(f"{phase}: readiness deadline exceeded")
        health = self.request("/api/healthz")
        status = self.request("/api/status")
        require(health == {"success": True}, "health probe failed")
        require(status.get("success") is True and status.get("data", {}).get("version") == version, "running binary version mismatch")
        (self.out / f"{phase}-health.json").write_text(json.dumps({"ready": True, "health": health, "version": version}) + "\n")

    def stop(self, phase):
        self.docker("stop", "--time", "30", self.app)
        self.docker("logs", self.app, evidence=f"{phase}-application.log")
        state = self.inspect("container", self.app)["State"]
        (self.out / f"{phase}-exit.json").write_text(json.dumps(state) + "\n")
        require(state["ExitCode"] == 0 and not state["OOMKilled"], f"{phase} did not shut down cleanly")

    def exercise(self, phase, previous_notice, candidate):
        # Tokens are generated only by this disposable app and are never stored in evidence.
        login = self.request("/api/user/login", "POST", {"username": "ci-upgrade", "password": PASSWORD})
        require(login.get("success") is True, "synthetic administrator login failed")
        bearer = login.get("data", {}).get("access_token")
        require(isinstance(bearer, str) and bool(bearer), "login did not issue a session token")
        user = self.request("/api/user/self", bearer=bearer)
        require(user.get("success") is True and user.get("data", {}).get("username") == "ci-upgrade", "existing admin identity cannot be read")
        notice = self.request("/api/notice")
        require(notice.get("success") is True and notice.get("data") == previous_notice, "application did not read the prior version's write")
        saved = self.request("/api/option/", "PUT", {"key": "Notice", "value": phase}, bearer)
        require(saved.get("success") is True, "application option write failed")
        require(self.request("/api/notice").get("data") == phase, "application option write could not be read back")
        monitor = self.request("/api/option/model-monitor", bearer=bearer)
        require(monitor.get("success") is True and monitor.get("data", {}).get("config") == MONITOR_CONFIG, "saved monitor visibility did not load")
        omitted = {key: value for key, value in MONITOR_CONFIG.items() if key != "hidden_groups"}
        monitor = self.request("/api/option/model-monitor", "PUT", omitted, bearer)
        require(monitor.get("success") is True and monitor.get("data") == MONITOR_CONFIG, "legacy omission-save lost hidden groups")
        models = self.request("/v1/models", bearer="sk-" + TOKEN)
        check_models(models, candidate)
        (self.out / f"{phase}-contracts.json").write_text(json.dumps({"admin_read_write": True, "previous_notice": previous_notice,
            "saved_notice": phase, "monitor_omission_save": True, "models": models}, indent=2) + "\n")

    def cleanup(self):
        # Names are freshly generated here, never supplied by a caller.
        for container in reversed(self.containers):
            self.docker("logs", container, check=False, evidence=container + "-cleanup.log")
            self.docker("rm", "--force", "--volumes", container, check=False)
        self.docker("network", "rm", self.network, check=False)
        for image in self.images:
            self.docker("image", "rm", image, check=False)


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument("--baseline-dir", required=True, type=Path)
    parser.add_argument("--candidate-dir", required=True, type=Path)
    parser.add_argument("--candidate-sha", required=True)
    parser.add_argument("--out", required=True, type=Path)
    args = parser.parse_args()
    baseline, candidate, out = args.baseline_dir.resolve(), args.candidate_dir.resolve(), args.out.resolve()
    require(baseline != candidate, "baseline and candidate must use separate checkouts")
    require(not out.is_relative_to(baseline) and not out.is_relative_to(candidate), "evidence must be outside source checkouts")
    out.mkdir(parents=True, exist_ok=True)
    require(not any(out.iterdir()), "evidence directory must be empty; stale results cannot be reused")
    versions = {"baseline": verify_checkout(baseline, BASELINE_SHA), "candidate": verify_checkout(candidate, args.candidate_sha)}
    recipe = candidate / "Dockerfile.api"
    manifest = {"scope": "synthetic source-built baseline; NOT deployed production artifact or representative production data",
                "baseline_sha": BASELINE_SHA, "candidate_sha": args.candidate_sha, "postgres_image": POSTGRES_IMAGE,
                "build_recipe_source_sha": args.candidate_sha, "build_recipe_sha256": hashlib.sha256(recipe.read_bytes()).hexdigest(),
                "baseline_recipe_difference": "candidate Dockerfile.api includes tokenkit/go.mod before go mod download; baseline source is unchanged",
                "versions": versions, "result": "running", "completed_phases": []}
    run = Lifecycle(out)
    try:
        run.docker("version", evidence="docker-version.txt")
        images = {}
        for label, path, sha in (("baseline", baseline, BASELINE_SHA), ("candidate", candidate, args.candidate_sha)):
            tag = "new-api:" + run.prefix + "-" + label
            run.images.append(tag)
            run.docker("build", "--progress=plain", "-f", str(recipe), "--build-arg", "SOURCE_REVISION=" + sha,
                       "--build-arg", "RELEASE_VERSION=" + versions[label], "--tag", tag, str(path), timeout=1800, evidence=label + "-build.log")
            inspected = run.inspect("image", tag, evidence=label + "-image.json")
            labels = inspected["Config"]["Labels"]
            require(labels.get("org.opencontainers.image.revision") == sha and labels.get("org.opencontainers.image.version") == versions[label], "built image provenance labels mismatch")
            images[label] = inspected["Id"]
        manifest["application_image_ids"] = images
        run.docker("pull", POSTGRES_IMAGE, timeout=300, evidence="postgres-pull.log")
        pg_image = run.inspect("image", POSTGRES_IMAGE, evidence="postgres-image.json")["Id"]
        manifest["postgres_image_id"] = pg_image
        run.docker("network", "create", "--internal", run.network)
        require(run.inspect("network", run.network, evidence="network.json")["Internal"] is True, "runtime network is not internal")
        run.containers.append(run.pg)
        run.docker("run", "--detach", "--pull=never", "--name", run.pg, "--network", run.network,
                   "--env", "POSTGRES_PASSWORD=synthetic", "--env", "POSTGRES_DB=synthetic_main", pg_image)
        deadline = time.monotonic() + 120
        while time.monotonic() < deadline:
            require(run.inspect("container", run.pg)["State"]["Running"], "PostgreSQL exited during startup")
            if run.docker("exec", run.pg, "pg_isready", "-h", "127.0.0.1", "-U", "postgres", "-d", "synthetic_main", check=False).returncode == 0:
                break
            time.sleep(1)
        else:
            raise RuntimeError("PostgreSQL readiness deadline exceeded")
        db_version = run.sql("synthetic_main", "SHOW server_version;")
        require(re.match(r"18\.6(?:\D|$)", db_version) is not None, "expected PostgreSQL 18.6")
        manifest["postgres_server_version"] = db_version
        run.docker("exec", run.pg, "createdb", "-U", "postgres", "synthetic_log")
        run.start(images["baseline"], "baseline-fresh", versions["baseline"])
        setup = run.request("/api/setup", "POST", {"username": "ci-upgrade", "password": PASSWORD, "confirmPassword": PASSWORD})
        require(setup.get("success") is True, "fresh baseline application setup failed")
        run.stop("baseline-fresh")
        manifest["completed_phases"].append("baseline-fresh")
        run.sql("synthetic_main", MAIN_SEED)
        run.sql("synthetic_log", LOG_SEED)
        initial = run.snapshot("seed")
        check_snapshot(initial, initial)
        previous_notice = "synthetic-seed"
        upgraded = None
        for phase, label in (("baseline-seeded", "baseline"), ("candidate-upgrade", "candidate"),
                             ("candidate-restart", "candidate"), ("baseline-rollback", "baseline")):
            run.start(images[label], phase, versions[label])
            run.exercise(phase, previous_notice, label == "candidate")
            run.stop(phase)
            current = run.snapshot(phase)
            check_snapshot(initial, current)
            if upgraded is not None:
                check_snapshot(upgraded, current, same_schema=phase == "candidate-restart")
            if phase == "candidate-upgrade":
                upgraded = current
            require(run.sql("synthetic_main", "SELECT value FROM options WHERE \"key\"='Notice';") == phase, "application write was not persisted after shutdown")
            previous_notice = phase
            manifest["completed_phases"].append(phase)
        verify_checkout(baseline, BASELINE_SHA)
        verify_checkout(candidate, args.candidate_sha)
        manifest["result"] = "passed"
        print("PASS: synthetic source-built baseline → candidate → candidate restart → baseline application rollback; separate PostgreSQL 18.6 main/log databases, read/write and preserved fixtures. NOT production-equivalence evidence.")
    except BaseException as error:
        manifest["result"] = "failed"
        manifest["error"] = str(error)
        raise
    finally:
        (out / "manifest.json").write_text(json.dumps(manifest, indent=2) + "\n")
        try:
            run.cleanup()
        except (OSError, subprocess.SubprocessError) as error:
            (out / "cleanup-error.txt").write_text(str(error) + "\n")
            if manifest["result"] == "passed":
                raise


if __name__ == "__main__":
    main()
