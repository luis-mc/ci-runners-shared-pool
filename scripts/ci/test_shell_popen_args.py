"""CRSP329: a gate step runs under bash on Windows when DELIVERY_SHELL=bash, else through the default shell."""
import os
import sys
import unittest
from unittest import mock

sys.path.insert(0, os.path.join(os.path.dirname(os.path.dirname(os.path.abspath(__file__))), "delivery"))
import delivery_common as dc  # noqa: E402

CMD = "{ true; } || echo no"


class ShellPopenArgs(unittest.TestCase):
    def test_default_is_the_platform_shell(self):
        with mock.patch.dict(os.environ, {}, clear=False):
            os.environ.pop("DELIVERY_SHELL", None)
            self.assertEqual(dc.shell_popen_args(CMD, None, which=lambda n: "/bin/bash"), {"args": CMD, "shell": True})

    def test_bash_on_windows_when_asked(self):
        with mock.patch.object(dc.os, "name", "nt"):
            got = dc.shell_popen_args(CMD, {"DELIVERY_SHELL": "bash"}, which=lambda n: "C:/git/bash.exe")
        self.assertEqual(got, {"args": ["C:/git/bash.exe", "-c", CMD]})

    def test_missing_bash_falls_back(self):
        with mock.patch.object(dc.os, "name", "nt"):
            got = dc.shell_popen_args(CMD, {"DELIVERY_SHELL": "bash"}, which=lambda n: None)
        self.assertEqual(got, {"args": CMD, "shell": True})

    def test_other_systems_ignore_it(self):
        with mock.patch.object(dc.os, "name", "posix"):
            got = dc.shell_popen_args(CMD, {"DELIVERY_SHELL": "bash"}, which=lambda n: "/bin/bash")
        self.assertEqual(got, {"args": CMD, "shell": True})


if __name__ == "__main__":
    unittest.main()
