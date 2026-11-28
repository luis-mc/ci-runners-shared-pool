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
        self.assertIn("needs: [guard, version, build, msi, installers-unix, agent-installers, agent-pkg]", text)
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


class ReleaseNotes(unittest.TestCase):
    """CRSP274: the release notes come from the change notes."""

    def make(self, d, notes, registry=None):
        for rel, text in notes.items():
            p = os.path.join(d, rel)
            os.makedirs(os.path.dirname(p), exist_ok=True)
            open(p, "w").write(text)
        if registry is not None:
            os.makedirs(os.path.join(d, "docs/project"), exist_ok=True)
            open(os.path.join(d, "docs/project/registry.json"), "w").write(registry)

    def run_notes(self, d, *args):
        r = subprocess.run(["python3", os.path.join(HERE, "release_notes.py"), "--root", d, *args], capture_output=True, text=True)
        self.assertEqual(r.returncode, 0, r.stderr)
        return r.stdout

    def test_groups_by_document_and_puts_major_first(self):
        with tempfile.TemporaryDirectory() as d:
            self.make(d, {
                "docs/features/cli/spec.md": "---\nid: cli\ntitle: CLI\n---\n# CLI\n",
                "docs/features/cli/changes/A1.md": "---\nchange: minor\ntask: A1\n---\nAdds a command.\n",
                "docs/features/cli/changes/A2.md": "---\nchange: major\ntask: A2\n---\nRemoves a flag\nof the tool.\n",
            })
            out = self.run_notes(d)
            self.assertTrue(out.startswith("## What's new\n\n**CLI**\n"))
            self.assertLess(out.index("(A2)"), out.index("(A1)"))
            self.assertIn("- Removes a flag of the tool. (A2)", out)

    def test_release_filter_follows_the_project_record(self):
        with tempfile.TemporaryDirectory() as d:
            reg = '{"tasks":[{"id":"T1","release":5},{"id":"T2","release":6},{"id":"T3","rides":"T1"}]}'
            self.make(d, {
                "docs/features/x/changes/T1.md": "---\nchange: minor\ntask: T1\n---\nOne.\n",
                "docs/features/x/changes/T2.md": "---\nchange: minor\ntask: T2\n---\nTwo.\n",
                "docs/features/x/changes/T3.md": "---\nchange: minor\ntask: T3\n---\nThree.\n",
                "docs/features/x/changes/T4.md": "---\nchange: minor\ntask: T4\n---\nFour.\n",
            }, reg)
            out = self.run_notes(d, "--release", "5")
            self.assertIn("(T1)", out)
            self.assertIn("(T3)", out)  # rides T1
            self.assertIn("(T4)", out)  # no number: kept, like docs_fold
            self.assertNotIn("(T2)", out)

    def test_no_notes_prints_nothing(self):
        with tempfile.TemporaryDirectory() as d:
            self.assertEqual(self.run_notes(d), "")

    def test_max_chars_keeps_major_notes_and_counts_the_rest(self):
        with tempfile.TemporaryDirectory() as d:
            notes = {"docs/features/x/changes/M1.md": "---\nchange: major\ntask: M1\n---\nBig change.\n"}
            for i in range(50):
                notes["docs/features/x/changes/N%02d.md" % i] = "---\nchange: minor\ntask: N%02d\n---\n%s\n" % (i, "word " * 40)
            self.make(d, notes)
            out = self.run_notes(d, "--max-chars", "2000")
            self.assertLessEqual(len(out), 2000)
            self.assertIn("(M1)", out)
            self.assertRegex(out, r"\n\d+ more change notes did not fit here")
            full = self.run_notes(d)
            self.assertNotIn("did not fit", full)

    def test_release_workflow_uses_the_change_notes(self):
        text = open(WORKFLOW).read()
        self.assertIn("python3 scripts/ci/release_notes.py", text)
        # GitHub refuses notes over 125,000 characters (v0.1.33 failed with HTTP 422).
        self.assertNotRegex(text, r"release_notes\.py;")
        self.assertIn("release_notes.py --max-chars", text)


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

    def test_agent_native_package_names(self):
        # CRSP348: msi, pkg and deb next to the archives; the archive names do not change
        def run(script):
            r = subprocess.run(["bash", "-c", 'source "%s"; %s' % (NAMES, script)], capture_output=True, text=True)
            self.assertEqual(r.returncode, 0, r.stderr)
            return r.stdout.strip()

        self.assertEqual(run("agent_asset_name 0.1.22 windows arm64 msi"), "CrspAgent-0.1.22-win-arm64.msi")
        self.assertEqual(run("agent_stable_to_asset 0.1.22 crspagent-linux-amd64.deb"), "CrspAgent-0.1.22-linux-x64.deb")
        self.assertEqual(run("agent_stable_to_asset 0.1.22 crspagent-darwin-amd64.pkg"), "CrspAgent-0.1.22-macos-x64.pkg")
        self.assertEqual(run("agent_stable_to_asset 0.1.22 crspagent-linux-amd64.tar.gz"), "CrspAgent-0.1.22-linux-x64.tar.gz")

    def test_release_builds_the_agent_native_packages(self):
        text = open(WORKFLOW).read()
        for want in (
            "bash installer/agent/linux/build.sh",
            "bash installer/agent/macos/build.sh",
            "./installer/agent/windows/build.ps1",
            "name: agent-msi-windows-${{ matrix.goarch }}",
            "name: agent-pkg-darwin-${{ matrix.arch }}",
            "-eq 12 ]",
            "-eq 26 ]",
            "-eq 30 ]",
        ):
            self.assertIn(want, text)

    def test_agent_deb_builds_on_linux(self):
        # CRSP348: nfpm runs on Linux, so the deb is built and read back here (skipped without nfpm).
        import shutil
        if not (shutil.which("dpkg-deb") and shutil.which("python3")):
            self.skipTest("dpkg-deb not found")
        with tempfile.TemporaryDirectory() as d:
            exe = os.path.join(d, "crsp")
            open(exe, "w").write("x")
            os.chmod(exe, 0o755)
            script = os.path.join(HERE, "..", "..", "installer", "agent", "linux", "build.sh")
            r = subprocess.run(["bash", script, "arm64", exe, "v1.2.3", "5", os.path.join(d, "out")], capture_output=True, text=True)
            if r.returncode != 0 and "nfpm" in r.stderr.lower():
                self.skipTest("nfpm not available: " + r.stderr[-200:])
            self.assertEqual(r.returncode, 0, r.stderr)
            deb = os.path.join(d, "out", "crspagent-linux-arm64.deb")
            info = subprocess.run(["dpkg-deb", "-f", deb], capture_output=True, text=True).stdout
            self.assertIn("Package: crsp-agent", info)
            self.assertIn("Architecture: arm64", info)
            self.assertIn("Version: 1.2.3-1", info)
            self.assertIn("Conflicts: crsp", info)
            files = subprocess.run(["dpkg-deb", "-c", deb], capture_output=True, text=True).stdout
            self.assertIn("./usr/bin/crsp", files)
            self.assertNotIn("systemd", files)

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

    def notes_dir(self, text, version="0.1.22"):
        d = tempfile.mkdtemp(dir=self.d)
        if text is not None:
            with open(os.path.join(d, version + ".txt"), "w", encoding="utf-8") as f:
                f.write(text)
        return d

    def test_note_is_carried_when_the_release_has_one(self):
        text = "Updates show live progress.  \n\nRunners have a filter bar.\n"
        r, out = self.build(extra=("--notes-dir", self.notes_dir(text)))
        self.assertEqual(r.returncode, 0, r.stderr)
        raw = open(out, "rb").read()
        m = json.loads(raw)
        self.assertEqual(m["notes"], "Updates show live progress.\n\nRunners have a filter bar.")
        self.assertEqual(raw, json.dumps(m, sort_keys=True, separators=(",", ":")).encode())

    def test_no_note_leaves_the_key_out(self):
        for text in (None, "  \n"):
            r, out = self.build(extra=("--notes-dir", self.notes_dir(text)))
            self.assertEqual(r.returncode, 0, r.stderr)
            self.assertNotIn("notes", json.load(open(out)))

    def test_a_note_that_would_not_verify_fails_the_release(self):
        for text in ("Caf\u00e9 rules", "x" * 601):
            r, _ = self.build(extra=("--notes-dir", self.notes_dir(text)))
            self.assertNotEqual(r.returncode, 0, text[:20])

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
