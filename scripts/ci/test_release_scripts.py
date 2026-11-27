import hashlib
import json
import os
import subprocess
import tempfile
import unittest

HERE = os.path.dirname(os.path.abspath(__file__))
GUARD = os.path.join(HERE, "release-guard.sh")
NOTES = os.path.join(HERE, "release-notes-fallback.sh")
NAMES = os.path.join(HERE, "release-names.sh")
MANIFEST = os.path.join(HERE, "release_manifest.py")
SIGN = os.path.join(HERE, "release-sign.sh")
WORKFLOW = os.path.join(HERE, "..", "..", ".github", "workflows", "release.yml")


class Repo(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.d = self.tmp.name
        self.git("init", "-q", "-b", "main")
        self.git("config", "user.email", "t@example.com")
        self.git("config", "user.name", "t")
        self.git("config", "commit.gpgsign", "false")

    def tearDown(self):
        self.tmp.cleanup()

    def git(self, *a):
        r = subprocess.run(["git", *a], cwd=self.d, capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, r.stderr)
        return r.stdout.strip()

    def commit(self, msg):
        self.git("commit", "-q", "--allow-empty", "-m", msg)
        return self.git("rev-parse", "HEAD")

    def run_script(self, *a):
        return subprocess.run(["bash", *a], cwd=self.d, capture_output=True, text=True)


class Guard(Repo):
    def test_commit_on_main_passes(self):
        sha = self.commit("a")
        self.assertEqual(self.run_script(GUARD, sha, "main").returncode, 0)

    def test_commit_on_unmerged_branch_refused(self):
        self.commit("a")
        self.git("checkout", "-q", "-b", "ops")
        sha = self.commit("unmerged")
        r = self.run_script(GUARD, sha, "main")
        self.assertEqual(r.returncode, 1)
        self.assertIn("not reachable from main", r.stderr)

    def test_missing_main_refused(self):
        sha = self.commit("a")
        r = self.run_script(GUARD, sha, "origin/main")
        self.assertEqual(r.returncode, 1)
        self.assertIn("fetch main first", r.stderr)


class Notes(Repo):
    def notes_file(self, text):
        p = os.path.join(self.d, ".git", "notes.txt")
        with open(p, "w") as f:
            f.write(text)
        return p

    def test_keeps_generated_list(self):
        self.commit("a")
        self.git("tag", "v0.1.0")
        self.commit("b")
        self.git("tag", "v0.1.1")
        r = self.run_script(NOTES, "v0.1.1", self.notes_file("## What's Changed\n* x"))
        self.assertEqual((r.returncode, r.stdout), (0, ""))

    def test_writes_subjects_between_tags(self):
        self.commit("a")
        self.git("tag", "v0.1.0")
        self.commit("fix one")
        self.commit("add two")
        self.git("tag", "v0.1.1")
        r = self.run_script(NOTES, "v0.1.1", self.notes_file("**Full Changelog**: x"))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn("- add two", r.stdout)
        self.assertIn("- fix one", r.stdout)
        self.assertNotIn("- a\n", r.stdout)
        self.assertIn("since v0.1.0", r.stdout)

    def test_first_release_lists_all(self):
        self.commit("only")
        self.git("tag", "v0.1.0")
        r = self.run_script(NOTES, "v0.1.0", self.notes_file(""))
        self.assertIn("- only", r.stdout)


class Workflow(unittest.TestCase):
    def test_every_job_waits_for_guard(self):
        text = open(WORKFLOW).read()
        self.assertIn("release-guard.sh", text)
        self.assertIn("needs: guard", text)  # version, which build and msi follow
        self.assertIn("needs: [guard, version, build, msi, installers-unix, agent-installers]", text)
        self.assertIn("release-notes-fallback.sh", text)

    def test_release_assets_are_named_crsp_version_os_arch(self):
        # OPS69: every release asset is Crsp-<version>-<os>-<arch>.<ext>, made by release-names.sh.
        text = open(WORKFLOW).read()
        self.assertIn('VERSION: ${{ needs.version.outputs.value }}', text)
        for want in (
            'asset_name "$VERSION" "$goos" "$goarch" zip',
            'asset_name "$VERSION" "$goos" "$goarch" tar.gz',
            'asset_name "$VERSION" windows "$arch" msi',
            'stable_to_asset "$VERSION" "$f"',
        ):
            self.assertIn(want, text)
        # The old version-suffix names are gone from the publish job.
        self.assertNotIn("_${VERSION}", text)
        # The CI artifacts keep their stable names (artifact-prune, DL-22).
        self.assertIn("name: msi-windows-${{ matrix.goarch }}", text)
        self.assertIn("name: pkg-${{ matrix.os }}-${{ matrix.arch }}", text)

    def test_release_signs_the_manifest(self):
        text = open(WORKFLOW).read()
        self.assertIn("CRSP_UPDATE_SIGNING_KEY: ${{ secrets.CRSP_UPDATE_SIGNING_KEY }}", text)
        self.assertIn("release_manifest.py", text)
        self.assertIn("release-sign.sh dist/manifest.json dist/manifest.json.sig", text)
        # The checksums cover the manifest and its signature; the attestation too.
        self.assertIn("manifest.json manifest.json.sig > SHA256SUMS", text)
        self.assertIn("dist/manifest.json.sig", text)

    def names(self, *args):
        r = subprocess.run(["bash", NAMES, *args], capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, r.stderr)
        return r.stdout.strip()

    def test_asset_names(self):
        self.assertEqual(self.names("0.1.22", "windows", "amd64", "msi"), "Crsp-0.1.22-win-x64.msi")
        self.assertEqual(self.names("0.1.22", "windows", "arm64", "msi"), "Crsp-0.1.22-win-arm64.msi")
        self.assertEqual(self.names("0.1.22", "linux", "amd64", "deb"), "Crsp-0.1.22-linux-x64.deb")
        self.assertEqual(self.names("0.1.22", "darwin", "arm64", "pkg"), "Crsp-0.1.22-macos-arm64.pkg")
        self.assertEqual(self.names("v0.1.22", "linux", "amd64", "tar.gz"), "Crsp-0.1.22-linux-x64.tar.gz")
        self.assertEqual(self.names("edge-abc1234", "windows", "amd64", "zip"), "Crsp-edge-abc1234-win-x64.zip")

    def test_unknown_os_or_arch_fails(self):
        r = subprocess.run(["bash", NAMES, "0.1.22", "plan9", "amd64", "msi"], capture_output=True, text=True)
        self.assertNotEqual(r.returncode, 0)
        r = subprocess.run(["bash", NAMES, "0.1.22", "linux", "riscv", "deb"], capture_output=True, text=True)
        self.assertNotEqual(r.returncode, 0)

    def test_native_installers_are_renamed(self):
        with tempfile.TemporaryDirectory() as d:
            os.makedirs(os.path.join(d, "pkgs", "pkg-linux-amd64"))
            os.makedirs(os.path.join(d, "pkgs", "pkg-darwin-arm64"))
            os.makedirs(os.path.join(d, "dist"))
            for sub, n in (
                ("pkg-linux-amd64", "crsp-linux-amd64.deb"),
                ("pkg-linux-amd64", "crsp-linux-amd64.rpm"),
                ("pkg-darwin-arm64", "crsp-darwin-arm64.pkg"),
            ):
                open(os.path.join(d, "pkgs", sub, n), "w").close()
            script = (
                'source "%s"; VERSION=0.1.22; for f in pkgs/*/crsp-*; do '
                'cp "$f" "dist/$(stable_to_asset "$VERSION" "$f")"; done; ls dist' % NAMES
            )
            r = subprocess.run(["bash", "-c", script], cwd=d, capture_output=True, text=True)
            self.assertEqual(r.returncode, 0, r.stderr)
            self.assertEqual(
                r.stdout.split(),
                ["Crsp-0.1.22-linux-x64.deb", "Crsp-0.1.22-linux-x64.rpm", "Crsp-0.1.22-macos-arm64.pkg"],
            )


class AgentInstallers(unittest.TestCase):
    """CRSP304: agent installers are CrspAgent-<version>-<os>-<arch>.<ext> release assets."""

    def test_agent_asset_names(self):
        def run(script):
            r = subprocess.run(["bash", "-c", 'source "%s"; %s' % (NAMES, script)], capture_output=True, text=True)
            self.assertEqual(r.returncode, 0, r.stderr)
            return r.stdout.strip()

        self.assertEqual(run("agent_asset_name v0.1.22 windows amd64"), "CrspAgent-0.1.22-win-x64.zip")
        self.assertEqual(run("agent_asset_name 0.1.22 linux arm64"), "CrspAgent-0.1.22-linux-arm64.tar.gz")
        self.assertEqual(run("agent_asset_name 0.1.22 darwin amd64"), "CrspAgent-0.1.22-macos-x64.tar.gz")
        self.assertEqual(run("agent_stable_to_asset 0.1.22 x/crspagent-darwin-arm64.tar.gz"), "CrspAgent-0.1.22-macos-arm64.tar.gz")
        self.assertEqual(run("agent_stable_to_asset 0.1.22 crspagent-windows-arm64.zip"), "CrspAgent-0.1.22-win-arm64.zip")

    def test_the_controller_name_stays_the_controller_name(self):
        r = subprocess.run(["bash", NAMES, "0.1.22", "windows", "amd64", "msi"], capture_output=True, text=True)
        self.assertEqual(r.stdout.strip(), "Crsp-0.1.22-win-x64.msi")

    def test_release_builds_checksums_and_attests_the_agent_installers(self):
        text = open(WORKFLOW).read()
        for want in (
            "agent-installers:",
            "bash installer/agent/build.sh",
            "name: agent-${{ matrix.os }}-${{ matrix.arch }}",
            'agent_stable_to_asset "$VERSION" "$f"',
            "sha256sum Crsp-* CrspAgent-* crsp-sbom-*",
            "dist/CrspAgent-*",
        ):
            self.assertIn(want, text)

    def test_manifest_ignores_agent_installers(self):
        # the update manifest lists the Controller MSIs only (release_manifest.py matches Crsp-*)
        text = open(os.path.join(HERE, "release_manifest.py")).read()
        self.assertIn('r"^Crsp-', text)

    def test_agent_build_script_makes_the_stable_names(self):
        with tempfile.TemporaryDirectory() as d:
            exe = os.path.join(d, "crsp")
            open(exe, "w").write("x")
            r = subprocess.run(["bash", os.path.join(HERE, "..", "..", "installer", "agent", "build.sh"), "linux", "arm64", exe, os.path.join(d, "out")],
                               capture_output=True, text=True)
            self.assertEqual(r.returncode, 0, r.stderr)
            self.assertTrue(os.path.isfile(os.path.join(d, "out", "crspagent-linux-arm64.tar.gz")))


def have_openssl3():
    r = subprocess.run(["openssl", "version"], capture_output=True, text=True)
    return r.returncode == 0 and r.stdout.startswith("OpenSSL 3")


class Manifest(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.d = self.tmp.name
        self.dist = os.path.join(self.d, "dist")
        os.makedirs(self.dist)
        for n, body in (
            ("Crsp-0.1.22-win-x64.msi", b"x64"),
            ("Crsp-0.1.22-win-arm64.msi", b"arm"),
            ("Crsp-0.1.22-linux-x64.deb", b"deb"),
        ):
            with open(os.path.join(self.dist, n), "wb") as f:
                f.write(body)

    def tearDown(self):
        self.tmp.cleanup()

    def build(self, version="0.1.22", extra=()):
        out = os.path.join(self.dist, "manifest.json")
        r = subprocess.run(
            ["python3", MANIFEST, "--version", version, "--channel", "stable", "--dist", self.dist,
             "--repo", "o/r", "--tag", "v" + version, "--now", "1000", "--out", out, *extra],
            capture_output=True, text=True,
        )
        return r, out

    def test_manifest_is_canonical_and_has_the_contract_keys(self):
        r, out = self.build()
        self.assertEqual(r.returncode, 0, r.stderr)
        raw = open(out, "rb").read()
        m = json.loads(raw)
        for k in ("product", "channel", "version", "required", "expires_at", "url", "sha256"):
            self.assertIn(k, m)
        self.assertEqual(raw, json.dumps(m, sort_keys=True, separators=(",", ":")).encode())
        self.assertEqual(m["version"], "0.1.22")
        self.assertFalse(m["required"])
        self.assertEqual(m["expires_at"], 1000 + 180 * 86400)
        self.assertEqual(m["url"], "https://github.com/o/r/releases/download/v0.1.22/Crsp-0.1.22-win-x64.msi")
        self.assertEqual(m["sha256"], hashlib.sha256(b"x64").hexdigest())
        self.assertEqual(sorted(m["assets"]), ["win-arm64", "win-x64"])

    def test_required_flag(self):
        r, out = self.build(extra=("--required",))
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertTrue(json.load(open(out))["required"])

    def test_a_file_with_another_version_fails(self):
        r, _ = self.build(version="0.1.23")
        self.assertNotEqual(r.returncode, 0)

    def test_no_x64_msi_fails(self):
        os.remove(os.path.join(self.dist, "Crsp-0.1.22-win-x64.msi"))
        r, _ = self.build()
        self.assertNotEqual(r.returncode, 0)

    @unittest.skipUnless(have_openssl3(), "needs OpenSSL 3 (Ed25519 pkeyutl -rawin)")
    def test_signature_verifies_and_missing_key_fails(self):
        _, out = self.build()
        key = os.path.join(self.d, "key.pem")
        pub = os.path.join(self.d, "pub.pem")
        subprocess.run(["openssl", "genpkey", "-algorithm", "ed25519", "-out", key], check=True)
        subprocess.run(["openssl", "pkey", "-in", key, "-pubout", "-out", pub], check=True)
        sig = os.path.join(self.dist, "manifest.json.sig")
        env = dict(os.environ, CRSP_UPDATE_SIGNING_KEY=open(key).read(), CRSP_UPDATE_PUBLIC_KEY_FILE=pub)  # secret-scan:ignore (env var name; value is a throwaway test key file)
        r = subprocess.run(["bash", SIGN, out, sig], capture_output=True, text=True, env=env)
        self.assertEqual(r.returncode, 0, r.stderr)
        raw_sig = bytes.fromhex(open(sig).read())
        self.assertEqual(len(raw_sig), 64)
        binsig = os.path.join(self.d, "sig.bin")
        open(binsig, "wb").write(raw_sig)
        ok = subprocess.run(["openssl", "pkeyutl", "-verify", "-rawin", "-pubin", "-inkey", pub, "-in", out, "-sigfile", binsig],
                            capture_output=True)
        self.assertEqual(ok.returncode, 0)
        # One changed byte in the manifest no longer verifies.
        tampered = os.path.join(self.d, "tampered.json")
        open(tampered, "wb").write(open(out, "rb").read().replace(b"0.1.22", b"0.1.21"))
        bad = subprocess.run(["openssl", "pkeyutl", "-verify", "-rawin", "-pubin", "-inkey", pub, "-in", tampered, "-sigfile", binsig],
                             capture_output=True)
        self.assertNotEqual(bad.returncode, 0)
        # A missing secret fails the release.
        env.pop("CRSP_UPDATE_SIGNING_KEY")
        r = subprocess.run(["bash", SIGN, out, os.path.join(self.d, "s2")], capture_output=True, text=True, env=env)
        self.assertNotEqual(r.returncode, 0)
        self.assertIn("CRSP_UPDATE_SIGNING_KEY", r.stdout + r.stderr)
        # A secret that is not the key the app trusts fails.
        other = os.path.join(self.d, "other.pem")
        subprocess.run(["openssl", "genpkey", "-algorithm", "ed25519", "-out", other], check=True)
        env["CRSP_UPDATE_SIGNING_KEY"] = open(other).read()
        r = subprocess.run(["bash", SIGN, out, os.path.join(self.d, "s3")], capture_output=True, text=True, env=env)
        self.assertNotEqual(r.returncode, 0)


if __name__ == "__main__":
    unittest.main()
