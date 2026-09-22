"""Apply only the four reviewed changes not yet written to source.
Fifteen other post-merge source/test corrections were committed directly.
This script does not certify tests or deploy anything. The integration workflow
formats, commits only this branch, then validates the exact candidate SHA.
"""
from pathlib import Path
import hashlib
import json
import os
import subprocess

expected = ('true', '1057300248/wanchuan-new-api', 'refs/heads/codex/rc40-full-integration-20260922')
actual = tuple(os.environ.get(key) for key in ('GITHUB_ACTIONS', 'GITHUB_REPOSITORY', 'GITHUB_REF'))
if actual != expected:
    raise SystemExit('Refusing execution outside the isolated integration branch')
subprocess.run(['git', 'merge-base', '--is-ancestor', 'a1ee61c51d727f74880d5570628e65f0c9b5177a', 'HEAD'], check=True)
files = {
    'controller/plugin_protocol_test.go': ('278b32f6f33c312169231052324edff804d63964', '32989402b1f1e90b3b4911d3183aa959839662f5'),
    'controller/relay.go': ('f26f7c89935974e8faa5c3336fada3ac362c8baf', 'e77f7c9524dc9c1d670d1ad086ba62a209ce53e4'),
    'web/src/features/channels/lib/header-passthrough.ts': ('48bd7e324eddf3a016db4cb0d9db062d482ca3bf', 'b8baa69e2f0d637630dd580308d74254fa2c8825'),
    'web/src/features/channels/lib/model-mapping-rules.ts': ('f9803a445f5050a3077947ee71b8bef31c432db8', '5ea6f014ed83ec80c2a429c52dd528685ea9daba'),
}
def blob(path):
    return subprocess.check_output(['git', 'hash-object', path], text=True).strip()
current = {path: blob(path) for path in files}
if all(current[path] == target for path, (_, target) in files.items()):
    print('Remaining changes are already present; validation is still required.')
else:
    for path, (old, _) in files.items():
        if current[path] != old:
            raise SystemExit('Unexpected source revision: ' + path)
    patch = Path('.github/rc40/remaining.patch').read_bytes()
    # Normalize patch section separators only; all resulting bytes are pinned.
    patch = patch.replace(b'\n diff --git ', b'\ndiff --git ')
    if hashlib.sha256(patch).hexdigest() != 'def844f7df42c1f8e59491d750c5db816f0ef0f6eb2a561265dae0f3d2781736':
        raise SystemExit('Reviewed patch checksum mismatch')
    subprocess.run(['git', 'apply', '--check', '-'], input=patch, check=True)
    subprocess.run(['git', 'apply', '-'], input=patch, check=True)
    for path, (_, target) in files.items():
        if blob(path) != target:
            raise SystemExit('Applied blob differs from the review: ' + path)
marker = Path('.github/rc40/followup-applied.json')
marker.write_text(json.dumps({'base_candidate': 'a1ee61c51d727f74880d5570628e65f0c9b5177a', 'remaining_patch_applied': True, 'acceptance': 'pending exact-SHA checks'}, indent=2) + '\n')
print('Four reviewed changes applied. No tests were disabled or declared successful.')
