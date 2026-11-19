import io
import sys
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path

spec_dir = Path(__file__).resolve().parents[1]
sys.path.insert(0, str(spec_dir / "scripts"))

from check_mirrors import MIRROR_PAIRS, check_mirrors


class TestMirrorPairOrientation(unittest.TestCase):
    def test_each_pair_lists_canonical_source_first(self):
        # schema/ is the canonical source (check_conformance._SCHEMA_DIR);
        # schemas/json/ is only the compatibility mirror. Both check_mirrors
        # and update_mirrors.py unpack pairs as (canonical, mirror), so the
        # canonical path must come first.
        self.assertTrue(MIRROR_PAIRS)
        for canonical_rel, mirror_rel in MIRROR_PAIRS:
            with self.subTest(pair=(canonical_rel, mirror_rel)):
                self.assertTrue(
                    canonical_rel.startswith("schema/"),
                    f"expected canonical path under schema/, got {canonical_rel!r}",
                )
                self.assertTrue(
                    mirror_rel.startswith("schemas/json/"),
                    f"expected mirror path under schemas/json/, got {mirror_rel!r}",
                )

    def test_missing_mirror_is_reported_as_mirror_not_canonical(self):
        # When the canonical file exists but the mirror is absent, the
        # diagnostic must name the missing *mirror* path. Before the fix the
        # reversed pair made this read "missing canonical file: schemas/json/...".
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
        # stale copy; the printed "mirror != canonical" ordering must reflect
        # that rather than inverting the two roles.
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


if __name__ == "__main__":
    unittest.main()
