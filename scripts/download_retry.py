"""Bounded retry of transient network errors for CI downloads."""
import http.client
import time
import urllib.error
import urllib.request


def get(url, limit):
    req = urllib.request.Request(url, headers={"User-Agent": "KurumiFRP-CI", "Accept": "application/vnd.github+json"})
    for attempt in range(3):
        try:
            with urllib.request.urlopen(req, timeout=60) as response:
                data = response.read(limit + 1)
            if len(data) > limit:
                raise RuntimeError("response exceeds size limit: " + url)
            return data
        except urllib.error.HTTPError as err:
            if err.code not in (429, 500, 502, 503, 504) or attempt == 2:
                raise
        except (urllib.error.URLError, TimeoutError, ConnectionError, http.client.IncompleteRead):
            if attempt == 2:
                raise
        print(f"Transient network failure; retrying ({attempt + 1}/2)", flush=True)
        time.sleep(2 ** attempt)
