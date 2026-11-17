"""Offline tests for the CLI install scripts (LOZA-SEC-015).

`install.sh` is run against a fake curl that serves a local release fixture,
so checksum verification and fail-closed behaviour are exercised without
network access. `install.ps1` is checked for the equivalent guards because
PowerShell is not available on every development host.
"""

import hashlib
import io
import json
import os
import pathlib
import subprocess
import tarfile
import tempfile
import unittest

HERE = pathlib.Path(__file__).resolve().parent
INSTALL_SH = HERE / "install.sh"
INSTALL_PS1 = HERE / "install.ps1"

API_URL = "https://api.github.com/repos/astraive/loza/releases"
ASSET_URL = (
    "https://github.com/astraive/loza/releases/download/cli/v9.9.9/"
    "loza_9.9.9_{os}_{arch}.tar.gz"
)

FAKE_CURL = """#!/bin/sh
url=""
out=""
while [ "$#" -gt 0 ]; do
  case "$1" in
    -fsSL|-f|-s|-S|-L) shift ;;
    --proto|--proto-redir) shift 2 ;;
    -o) out="$2"; shift 2 ;;
    http://*|https://*) url="$1"; shift ;;
    *) echo "fake curl: unexpected argument: $1" >&2; exit 2 ;;
  esac
done
if [ -n "${FAKE_CURL_LOG:-}" ]; then printf '%s\\n' "$url" >> "$FAKE_CURL_LOG"; fi
if [ "$url" = "$FAKE_API_URL" ]; then
  cat "$FAKE_RELEASES" > "$out"
elif [ "$url" = "$FAKE_ASSET_URL" ]; then
  if [ "${FAKE_MODE:-}" = "tampered" ]; then
    cat "$FAKE_TAMPERED_ARCHIVE" > "$out"
  else
    cat "$FAKE_ARCHIVE" > "$out"
  fi
elif [ "$url" = "$FAKE_ASSET_URL.sha256" ]; then
  if [ "${FAKE_MODE:-}" = "missing" ]; then
    echo "fake curl: checksum asset not found" >&2
    exit 22
  fi
  cat "$FAKE_CHECKSUM" > "$out"
else
  echo "fake curl: unexpected URL: $url" >&2
  exit 22
fi
"""


def _target():
    system = subprocess.check_output(["uname", "-s"], text=True).strip().lower()
    machine = subprocess.check_output(["uname", "-m"], text=True).strip()
    os_name = "linux" if system == "linux" else "darwin"
    arch = "amd64" if machine in ("x86_64", "amd64") else "arm64"
    return os_name, arch


def _make_archive(path, payload):
    info = tarfile.TarInfo("loza")
    info.size = len(payload)
    info.mode = 0o755
    with tarfile.open(path, "w:gz") as archive:
        archive.addfile(info, io.BytesIO(payload))


class InstallShTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        root = pathlib.Path(self.tmp.name)
        self.bin = root / "bin"
        self.bin.mkdir()
        (self.bin / "curl").write_text(FAKE_CURL, encoding="utf-8")
        (self.bin / "curl").chmod(0o755)

        os_name, arch = _target()
        self.asset_url = ASSET_URL.format(os=os_name, arch=arch)
        self.asset_name = self.asset_url.rsplit("/", 1)[-1]

        self.good = root / "good.tar.gz"
        _make_archive(self.good, b"#!/bin/sh\necho loza-good\n")
        self.tampered = root / "tampered.tar.gz"
        _make_archive(self.tampered, b"#!/bin/sh\necho loza-tampered\n")

        digest = hashlib.sha256(self.good.read_bytes()).hexdigest()
        self.checksum = root / (self.asset_name + ".sha256")
        self.checksum.write_text(f"{digest}  {self.asset_name}\n", encoding="utf-8")

        self.releases = root / "releases.json"
        self.curl_log = root / "curl.log"
        self.install_dir = root / "installed"
        self._write_releases(self.asset_url)

    def _write_releases(self, asset_url):
        self.releases.write_text(
            json.dumps(
                [
                    {
                        "tag_name": "cli/v9.9.9",
                        "assets": [
                            {
                                "name": self.asset_name,
                                "browser_download_url": asset_url,
                            }
                        ],
                    }
                ],
                indent=2,
            ),
            encoding="utf-8",
        )

    def _run(self, mode="", repo="astraive/loza"):
        env = os.environ.copy()
        env["PATH"] = os.pathsep.join([str(self.bin), env.get("PATH", "")])
        env["LOZA_INSTALL_DIR"] = str(self.install_dir)
        env["LOZA_REPO"] = repo
        env["FAKE_API_URL"] = API_URL
        env["FAKE_ASSET_URL"] = self.asset_url
        env["FAKE_RELEASES"] = str(self.releases)
        env["FAKE_ARCHIVE"] = str(self.good)
        env["FAKE_TAMPERED_ARCHIVE"] = str(self.tampered)
        env["FAKE_CHECKSUM"] = str(self.checksum)
        env["FAKE_CURL_LOG"] = str(self.curl_log)
        env["FAKE_MODE"] = mode
        result = subprocess.run(
            ["sh", str(INSTALL_SH)],
            env=env,
            capture_output=True,
            text=True,
            timeout=60,
        )
        return result

    def _logged_urls(self):
        if not self.curl_log.exists():
            return []
        return self.curl_log.read_text(encoding="utf-8").splitlines()

    def test_valid_checksum_allows_install(self):
        result = self._run()
        self.assertEqual(result.returncode, 0, result.stderr)
        installed = self.install_dir / "loza"
        self.assertTrue(installed.exists())
        self.assertIn(b"loza-good", installed.read_bytes())
        self.assertIn("SHA-256 verified", result.stdout)
        self.assertEqual(len(self._logged_urls()), 3)

    def test_compact_json_release_with_v_tag(self):
        # GitHub's API returns compact JSON and current releases use "v<version>".
        self.releases.write_text(
            json.dumps(
                [
                    {
                        "tag_name": "v9.9.9",
                        "assets": [
                            {
                                "name": self.asset_name,
                                "browser_download_url": self.asset_url,
                            }
                        ],
                    }
                ],
                separators=(",", ":"),
            ),
            encoding="utf-8",
        )
        result = self._run()
        self.assertEqual(result.returncode, 0, result.stderr)
        self.assertTrue((self.install_dir / "loza").exists())

    def test_tampered_asset_fails_without_installing(self):
        result = self._run(mode="tampered")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("checksum mismatch", result.stderr)
        self.assertFalse((self.install_dir / "loza").exists())

    def test_missing_checksum_fails_without_installing(self):
        result = self._run(mode="missing")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("could not download release checksum", result.stderr)
        self.assertFalse((self.install_dir / "loza").exists())

    def test_malformed_checksum_fails(self):
        self.checksum.write_text("not-a-digest  whatever\n", encoding="utf-8")
        result = self._run()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("malformed release checksum", result.stderr)
        self.assertFalse((self.install_dir / "loza").exists())

    def test_repo_override_cannot_redirect(self):
        result = self._run(repo="evil/loza")
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("arbitrary repositories are not allowed", result.stderr)
        self.assertEqual(self._logged_urls(), [])
        self.assertFalse((self.install_dir / "loza").exists())

    def test_http_asset_url_is_rejected(self):
        self._write_releases(self.asset_url.replace("https://", "http://", 1))
        result = self._run()
        self.assertNotEqual(result.returncode, 0)
        self.assertIn("non-HTTPS or untrusted asset URL", result.stderr)
        self.assertEqual(len(self._logged_urls()), 1)
        self.assertFalse((self.install_dir / "loza").exists())


class InstallPs1GuardsTests(unittest.TestCase):
    """PowerShell is unavailable on most dev hosts; assert the source guards."""

    def test_powershell_installer_has_equivalent_guards(self):
        text = INSTALL_PS1.read_text(encoding="utf-8")
        for needle in (
            "LOZA_REPO must be",
            "-ine $DefaultRepo",
            "UriSchemeHttps",
            '"github.com"',
            ".sha256",
            "Get-FileHash -Algorithm SHA256",
            "Checksum mismatch",
            "Malformed release checksum",
            "checksum is for a different asset",
        ):
            with self.subTest(needle=needle):
                self.assertIn(needle, text)


if __name__ == "__main__":
    unittest.main()
