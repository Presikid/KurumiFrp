import io
import unittest
import urllib.error
from unittest.mock import patch

import download_retry


class DownloadRetryTests(unittest.TestCase):
    @patch("download_retry.time.sleep")
    @patch("download_retry.urllib.request.urlopen")
    def test_transient_failure_retries_and_returns_data(self, opener, sleep):
        opener.side_effect = [TimeoutError("timeout"), io.BytesIO(b"verified later")]
        self.assertEqual(download_retry.get("https://example.com/asset", 20), b"verified later")
        self.assertEqual(opener.call_count, 2)
        sleep.assert_called_once_with(1)

    @patch("download_retry.time.sleep")
    @patch("download_retry.urllib.request.urlopen")
    def test_permanent_error_is_not_retried(self, opener, sleep):
        opener.side_effect = urllib.error.HTTPError("https://example.com/asset", 404, "missing", {}, None)
        with self.assertRaises(urllib.error.HTTPError):
            download_retry.get("https://example.com/asset", 20)
        self.assertEqual(opener.call_count, 1)
        sleep.assert_not_called()

    @patch("download_retry.time.sleep")
    @patch("download_retry.urllib.request.urlopen")
    def test_size_limit_is_not_bypassed(self, opener, sleep):
        opener.return_value = io.BytesIO(b"too large")
        with self.assertRaises(RuntimeError):
            download_retry.get("https://example.com/asset", 2)
        self.assertEqual(opener.call_count, 1)
        sleep.assert_not_called()

    @patch("download_retry.time.sleep")
    @patch("download_retry.urllib.request.urlopen")
    def test_transient_error_stops_after_three_attempts(self, opener, sleep):
        opener.side_effect = TimeoutError("timeout")
        with self.assertRaises(TimeoutError):
            download_retry.get("https://example.com/asset", 20)
        self.assertEqual(opener.call_count, 3)
        self.assertEqual(sleep.call_count, 2)


if __name__ == "__main__":
    unittest.main()
