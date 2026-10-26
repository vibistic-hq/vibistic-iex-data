"""Tests for tools/validate.py helpers that need no network or key (run with
python3 -m unittest tools/test_validate.py). Every figure here is made up."""
import importlib.util
import pathlib
import unittest
import urllib.error
import urllib.request

spec = importlib.util.spec_from_file_location("validate", pathlib.Path(__file__).with_name("validate.py"))
validate = importlib.util.module_from_spec(spec)
spec.loader.exec_module(validate)


class Ratio(unittest.TestCase):
    # Security audit run-1 and Codex review of #17: plain decimals with every
    # significant digit, never exponent form, never rounded to six places.
    def test_plain_decimals(self):
        cases = {1e-7: "0.0000001", 1.5e-6: "0.0000015", 0.654321: "0.654321", 12.5: "12.5", 3.0: "3", 1000.0: "1000"}
        for x, want in cases.items():
            self.assertEqual(validate.ratio(x), want)
            self.assertNotIn("e", validate.ratio(x))


class NoRedirect(unittest.TestCase):
    def test_redirect_is_refused(self):
        req = urllib.request.Request("https://data.alpaca.markets/v2/x", headers={"APCA-API-KEY-ID": "dummy"})
        with self.assertRaises(urllib.error.HTTPError):
            validate.NoRedirect().redirect_request(req, None, 302, "Found", {}, "https://elsewhere.example/")


if __name__ == "__main__":
    unittest.main()
