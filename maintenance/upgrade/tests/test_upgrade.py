import importlib.util
import tempfile
import unittest
from pathlib import Path

HERE = Path(__file__).resolve().parents[1]
SPEC = importlib.util.spec_from_file_location("upgrade", HERE / "scripts/upgrade.py")
upgrade = importlib.util.module_from_spec(SPEC)
SPEC.loader.exec_module(upgrade)


class UpgradeToolTests(unittest.TestCase):
    def test_manifest_is_sha256_pinned_and_unique(self):
        manifest = upgrade.load_manifest()
        self.assertEqual(len(manifest["modules"]), 5)
        self.assertEqual(len({m["id"] for m in manifest["modules"]}), 5)

    def test_verify_does_not_create_worktree(self):
        result = upgrade.run("verify", None, None, None)
        self.assertEqual(result["status"], "verified")
        self.assertEqual(result["live_changes"], 0)
        self.assertEqual(result["secret_access"], 0)

    def test_invalid_digest_is_rejected(self):
        with tempfile.TemporaryDirectory() as tmp:
            original = (HERE / "modules.json").read_text()
            altered = original.replace(
                '"patch_sha256": "5b351cf2721b3b6cf5973747e2544c83e1390f421e6d52cadf25b737d084c605"',
                '"patch_sha256": "0b351cf2721b3b6cf5973747e2544c83e1390f421e6d52cadf25b737d084c605"',
            )
            path = Path(tmp) / "modules.json"
            path.write_text(altered)
            with self.assertRaises(upgrade.UpgradeError):
                upgrade.load_manifest(path)

    def test_wrong_upstream_sha_fails_closed(self):
        with self.assertRaises(upgrade.UpgradeError):
            upgrade.verify_target("wfo-upstream-v3.20.1", "0" * 40)

    def test_existing_output_rejected_without_modification(self):
        with tempfile.TemporaryDirectory() as tmp:
            with self.assertRaises(upgrade.UpgradeError):
                upgrade.prepare_output(Path(tmp))

    def test_inside_repository_output_is_rejected(self):
        with self.assertRaises(upgrade.UpgradeError):
            upgrade.prepare_output(upgrade.REPO_ROOT / "maintenance" / "upgrade" / "new-worktree")

    def test_unsafe_ref_rejected(self):
        with self.assertRaises(upgrade.UpgradeError):
            upgrade.verify_target("../unsafe", "a" * 40)


if __name__ == "__main__":
    unittest.main()
