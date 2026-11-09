"""One-time correction of the 0.7.1.3 published notes, preserving other edits."""
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[1]
if (root / "VERSION").read_text(encoding="utf-8").strip() != "0.7.1.4":
    print("One-time release-note correction is not applicable to this version.")
    raise SystemExit(0)
notes = root / "docs/RELEASE_0.7.1.3.md"
new = notes.read_text(encoding="utf-8")
old = subprocess.check_output(
    ["gh", "release", "view", "v0.7.1.3", "--json", "body", "--jq", ".body"],
    cwd=root, text=True, encoding="utf-8")
if old.rstrip("\n") == new.rstrip("\n"):
    print("Release notes already corrected.")
elif old.rstrip("\n") == ("# KurumiFRP 0.7.1.3\n\n" + new).rstrip("\n"):
    subprocess.run(["gh", "release", "edit", "v0.7.1.3", "--notes-file", str(notes)],
                   cwd=root, check=True)
else:
    raise SystemExit("Published release notes changed; retained them without overwriting.")
