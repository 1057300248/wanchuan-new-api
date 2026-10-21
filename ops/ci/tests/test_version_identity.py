import os
from pathlib import Path
import subprocess
import tempfile
import textwrap
import unittest

ROOT = Path(__file__).resolve().parents[3]
WORKFLOW = ROOT / ".github/workflows/docker-image-branch.yml"


class VersionIdentityTests(unittest.TestCase):
    def stamp(self, version, sha="a" * 40):
        workflow = WORKFLOW.read_text()
        block = workflow.split("      - name: Stamp source version and commit\n", 1)[1]
        block = block.split("      - name: Set up Docker Buildx", 1)[0]
        script = textwrap.dedent(block.split("        run: |\n", 1)[1])
        with tempfile.TemporaryDirectory() as directory:
            root = Path(directory)
            (root / "VERSION").write_text(version)
            envfile = root / "environment"
            result = subprocess.run(["bash", "-c", script], cwd=root,
                                    env={**os.environ, "COMMIT_SHA": sha, "GITHUB_ENV": str(envfile)},
                                    text=True, capture_output=True, timeout=10)
            return result, (root / "VERSION").read_text(), envfile.read_text() if envfile.exists() else ""

    def test_rc41_source_version_and_commit_are_preserved(self):
        version = (ROOT / "VERSION").read_text().strip()
        self.assertTrue(version.startswith("v1.0.0-rc.41.chuanapi."))
        result, stamped, environment = self.stamp(version + "\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        expected = version + "+gaaaaaaaaaaaa"
        self.assertEqual(stamped, expected + "\n")
        self.assertEqual(environment, "BINARY_VERSION=" + expected + "\n")

    def test_existing_build_metadata_is_extended_not_duplicated(self):
        result, stamped, _ = self.stamp("v1.0.0-rc.41+verified\n")
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertEqual(stamped, "v1.0.0-rc.41+verified.gaaaaaaaaaaaa\n")
        self.assertEqual(stamped.count("+"), 1)

    def test_bad_identity_does_not_write_version_or_environment(self):
        for version, sha in [("", "a" * 40), ("v1.0.0\necho unsafe", "a" * 40),
                             ("$(touch unsafe)", "a" * 40), ("v1.0.0", "main")]:
            with self.subTest(version=version, sha=sha):
                result, unchanged, environment = self.stamp(version, sha)
                self.assertNotEqual(result.returncode, 0)
                self.assertEqual(unchanged, version)
                self.assertEqual(environment, "")

    def test_image_metadata_matches_binary_without_changing_tag_scheme(self):
        text = WORKFLOW.read_text()
        self.assertIn("org.opencontainers.image.version=${{ env.BINARY_VERSION }}", text)
        self.assertIn("org.opencontainers.image.revision=${{ needs.prepare.outputs.sha }}", text)
        self.assertIn("SOURCE_REVISION=${{ needs.prepare.outputs.sha }}", text)
        self.assertIn("RELEASE_VERSION=${{ env.BINARY_VERSION }}", text)
        self.assertIn("${{ needs.prepare.outputs.version }}-${{ matrix.arch }}", text)
        self.assertNotIn('echo "${{ needs.prepare.outputs.version }}" > VERSION', text)


if __name__ == "__main__":
    unittest.main()
