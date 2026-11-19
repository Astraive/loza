import contextlib
import importlib.util
import io
import re
import sys
import unittest
from pathlib import Path

spec_dir = Path(__file__).resolve().parents[1]
runner_path = spec_dir / "conformance" / "runner.py"

_runner_spec = importlib.util.spec_from_file_location("conformance_runner", runner_path)
runner = importlib.util.module_from_spec(_runner_spec)
sys.modules["conformance_runner"] = runner
_runner_spec.loader.exec_module(runner)

MATRIX_SDKS = ("go", "python", "rust", "javascript", "lql")
GROUP_COLUMN_WIDTH = 24


def _matrix_lines():
    buffer = io.StringIO()
    with contextlib.redirect_stdout(buffer):
        runner._print_matrix()
    return buffer.getvalue().splitlines()


class TestConformanceMatrixAlignment(unittest.TestCase):
    def test_header_and_rows_have_equal_width(self):
        lines = _matrix_lines()
        header = lines[0]
        for row in lines[2:]:
            self.assertEqual(len(row), len(header), f"row width mismatch: {row!r}")

    def test_data_cells_align_with_sdk_headers(self):
        lines = _matrix_lines()
        header = lines[0]
        # Right-hand boundary of each SDK label in the header.
        header_ends = [header.index(sdk) + len(sdk) for sdk in MATRIX_SDKS]

        for row in lines[2:]:
            # Data cells occupy the columns after the group label. Each cell
            # is one token ("yes" or "-") right-justified in its SDK column.
            data_cells = row[GROUP_COLUMN_WIDTH:]
            data_ends = [match.end() + GROUP_COLUMN_WIDTH
                         for match in re.finditer(r"yes|-", data_cells)]
            self.assertEqual(data_ends, header_ends, f"misaligned row: {row!r}")


if __name__ == "__main__":
    unittest.main()
