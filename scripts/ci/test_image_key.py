import json
import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.abspath(__file__)))
import image_key as k  # noqa: E402

KEY = "a" * 64


def make_tree(files, pin="1.65.1"):
    root = tempfile.mkdtemp()
    files = dict(files)
    files.setdefault(
        "internal/runners/layers.go", 'const PlaywrightPin = "%s"\n' % pin
    )
    for rel, data in files.items():
        path = os.path.join(root, *rel.split("/"))
        os.makedirs(os.path.dirname(path), exist_ok=True)
        with open(path, "wb") as fh:
            fh.write(data if isinstance(data, bytes) else data.encode())
    return root


BASE = {
    "images/linux-base/Dockerfile": "FROM ubuntu\nRUN a\n",
    "images/layers/browsers/Dockerfile": "FROM x\n",
}


class KeyTest(unittest.TestCase):
    def test_deterministic(self):
        a = make_tree(BASE)
        b = make_tree(BASE)
        self.assertEqual(k.compute_key(a, "amd64"), k.compute_key(b, "amd64"))
        self.assertEqual(len(k.compute_key(a, "amd64")), 64)

    def test_crlf_checkout_gives_the_same_key(self):
        crlf = {p: c.replace("\n", "\r\n") for p, c in BASE.items()}
        self.assertEqual(
            k.compute_key(make_tree(BASE), "amd64"),
            k.compute_key(make_tree(crlf), "amd64"),
        )

    def test_binary_is_not_normalised(self):
        a = make_tree(dict(BASE, **{"images/x.bin": b"a\0\r\nb"}))
        b = make_tree(dict(BASE, **{"images/x.bin": b"a\0\nb"}))
        self.assertNotEqual(k.compute_key(a, "amd64"), k.compute_key(b, "amd64"))

    def test_file_content_path_pin_arch_and_extra_change_the_key(self):
        base = k.compute_key(make_tree(BASE), "amd64")
        edited = dict(BASE, **{"images/linux-base/Dockerfile": "FROM ubuntu\nRUN b\n"})
        added = dict(BASE, **{"images/new.txt": "x"})
        self.assertNotEqual(base, k.compute_key(make_tree(edited), "amd64"))
        self.assertNotEqual(base, k.compute_key(make_tree(added), "amd64"))
        self.assertNotEqual(base, k.compute_key(make_tree(BASE, pin="1.66.0"), "amd64"))
        self.assertNotEqual(base, k.compute_key(make_tree(BASE), "arm64"))
        self.assertNotEqual(base, k.compute_key(make_tree(BASE), "amd64", ["p=1"]))

    def test_renamed_file_changes_the_key(self):
        a = make_tree({"images/a.txt": "same"})
        b = make_tree({"images/b.txt": "same"})
        self.assertNotEqual(k.compute_key(a, "amd64"), k.compute_key(b, "amd64"))

    def test_extra_order_does_not_matter(self):
        t = make_tree(BASE)
        self.assertEqual(
            k.compute_key(t, "amd64", ["a=1", "b=2"]),
            k.compute_key(t, "amd64", ["b=2", "a=1"]),
        )

    def test_walk_order_does_not_matter(self):
        names = ["images/z.txt", "images/a/b.txt", "images/a.txt"]
        one = make_tree(dict((n, n) for n in names))
        two = make_tree(dict((n, n) for n in reversed(names)))
        self.assertEqual(k.compute_key(one, "amd64"), k.compute_key(two, "amd64"))

    def test_crsp_binary_and_files_outside_images_are_not_inputs(self):
        base = k.compute_key(make_tree(BASE), "amd64")
        more = dict(BASE, **{"images/linux-base/crsp": b"\0binary", "README.md": "x"})
        self.assertEqual(base, k.compute_key(make_tree(more), "amd64"))

    def test_missing_pin_stops(self):
        root = make_tree(BASE, pin="1")
        with open(os.path.join(root, "internal", "runners", "layers.go"), "w") as fh:
            fh.write("package runners\n")
        with self.assertRaises(SystemExit):
            k.compute_key(root, "amd64")

    def test_the_real_repository_has_a_key(self):
        root = os.path.abspath(os.path.join(os.path.dirname(__file__), "..", ".."))
        self.assertEqual(len(k.compute_key(root, "amd64")), 64)


def labels(**kw):
    base = {k.LABEL_REF: k.MAIN_REF, k.LABEL_RUN: "123", k.LABEL_INPUTS: KEY}
    base.update(kw)
    return {"config": {"Labels": base}}


def fake_run(manifest, config):
    def run(cmd):
        if "{{json .Manifest}}" in cmd:
            return json.dumps(manifest)
        return json.dumps(config)

    return run


class LookupTest(unittest.TestCase):
    def test_hit_when_built_by_main(self):
        run = fake_run({"digest": "sha256:abc"}, labels())
        self.assertEqual(k.lookup("r", KEY, run), (True, "sha256:abc", "123"))

    def test_platform_map_is_read(self):
        run = fake_run({"digest": "sha256:abc"}, {"linux/amd64": labels()})
        self.assertTrue(k.lookup("r", KEY, run)[0])

    def test_pull_request_or_foreign_image_is_a_miss(self):
        for bad in (
            labels(**{k.LABEL_REF: "refs/pull/1/merge"}),
            labels(**{k.LABEL_INPUTS: "b" * 64}),
            labels(**{k.LABEL_RUN: ""}),
            labels(**{k.LABEL_RUN: "x1"}),
            {"config": {}},
            {},
        ):
            self.assertFalse(k.lookup("r", KEY, fake_run({"digest": "d"}, bad))[0])

    def test_any_error_is_a_miss(self):
        def boom(cmd):
            raise OSError("no docker")

        self.assertEqual(k.lookup("r", KEY, boom), (False, "", ""))

        def junk(cmd):
            return "not json"

        self.assertFalse(k.lookup("r", KEY, junk)[0])


if __name__ == "__main__":
    unittest.main()
