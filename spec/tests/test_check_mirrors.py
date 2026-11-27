import io
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path
from unittest.mock import patch

spec_dir = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(spec_dir / "scripts"))

from check_mirrors import MIRROR_PAIRS, check_mirrors
import update_mirrors


class TestMirrorPairOrientation(unittest.TestCase):
    def test_each_pair_lists_canonical_source_first(self):
        # schemas/json/ is canonical; schema/ is the deprecated mirror.
        # Both scripts unpack pairs as (canonical, mirror), so the canonical
        # path must come first.
        self.assertTrue(MIRROR_PAIRS)
        for canonical_rel, mirror_rel in MIRROR_PAIRS:
            with self.subTest(pair=(canonical_rel, mirror_rel)):
                self.assertTrue(
                    canonical_rel.startswith("schemas/json/"),
                    f"expected canonical path under schemas/json/, got {canonical_rel!r}",
                )
                self.assertTrue(
                    mirror_rel.startswith("schema/"),
                    f"expected mirror path under schema/, got {mirror_rel!r}",
                )

    def test_missing_mirror_is_reported_as_mirror_not_canonical(self):
        # When the canonical file exists but the mirror is absent, the
        # diagnostic must name the missing *mirror* path.
        canonical_rel, mirror_rel = MIRROR_PAIRS[0]
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            canonical = root / canonical_rel
            canonical.parent.mkdir(parents=True, exist_ok=True)
            canonical.write_text("{}\n", encoding="utf-8")

            output = io.StringIO()
            with redirect_stdout(output):
                ok = check_mirrors(root, [(canonical_rel, mirror_rel)])

            message = output.getvalue()
        self.assertFalse(ok)
        self.assertIn(f"missing mirror file: {mirror_rel}", message)
        self.assertNotIn(f"missing canonical file: {mirror_rel}", message)

    def test_drift_message_names_canonical_and_mirror_correctly(self):
        # On drift, the canonical path is the source and the mirror is the
        # stale copy; the printed "mirror != canonical" ordering reflects roles.
        canonical_rel, mirror_rel = MIRROR_PAIRS[0]
        with tempfile.TemporaryDirectory() as td:
            root = Path(td)
            canonical = root / canonical_rel
            canonical.parent.mkdir(parents=True, exist_ok=True)
            canonical.write_text('{"v": "canonical"}\n', encoding="utf-8")
            mirror = root / mirror_rel
            mirror.parent.mkdir(parents=True, exist_ok=True)
            mirror.write_text('{"v": "mirror"}\n', encoding="utf-8")

            output = io.StringIO()
            with redirect_stdout(output):
                ok = check_mirrors(root, [(canonical_rel, mirror_rel)])

            message = output.getvalue()
        self.assertFalse(ok)
        self.assertIn(f"drift detected: {mirror_rel} != {canonical_rel}", message)

    def test_updater_preserves_canonical_and_overwrites_mirror(self):
        with tempfile.TemporaryDirectory() as td:
            spec_root = Path(td) / "spec"
            for canonical_rel, mirror_rel in MIRROR_PAIRS:
                canonical = spec_root / canonical_rel
                canonical.parent.mkdir(parents=True, exist_ok=True)
                canonical.write_text(f"canonical:{canonical_rel}\n", encoding="utf-8")

                mirror = spec_root / mirror_rel
                mirror.parent.mkdir(parents=True, exist_ok=True)
                mirror.write_text(f"stale-mirror:{mirror_rel}\n", encoding="utf-8")

            updater_file = spec_root / "scripts" / "update_mirrors.py"
            with patch.object(update_mirrors, "__file__", str(updater_file)):
                self.assertEqual(update_mirrors.main(), 0)

            for canonical_rel, mirror_rel in MIRROR_PAIRS:
                canonical = spec_root / canonical_rel
                mirror = spec_root / mirror_rel
                self.assertEqual(
                    canonical.read_text(encoding="utf-8"),
                    f"canonical:{canonical_rel}\n",
                )
                self.assertEqual(
                    mirror.read_text(encoding="utf-8"),
                    f"canonical:{canonical_rel}\n",
                )


if __name__ == "__main__":
    unittest.main()
