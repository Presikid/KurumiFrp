"""Reject a redundant release title in the current version notes."""
import re
from pathlib import Path

root = Path(__file__).resolve().parents[1]
version = (root / "VERSION").read_text(encoding="utf-8").strip()
notes = (root / "docs" / f"RELEASE_{version}.md").read_text(encoding="utf-8")
duplicate = re.compile(r"^\s*#{1,6}\s+KurumiFRP\s+v?" + re.escape(version) + r"(?:\s|$)", re.IGNORECASE | re.MULTILINE)
if duplicate.search(notes):
    raise SystemExit("Release notes must not repeat the release title and version.")
if not notes.strip():
    raise SystemExit("Release notes must not be empty.")
