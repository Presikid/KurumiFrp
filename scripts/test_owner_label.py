"""Exercise the actual embedded shell with a stub GitHub CLI; never calls GitHub."""
import os
from pathlib import Path
import shutil
import subprocess
import tempfile
import unittest


ROOT = Path(__file__).resolve().parents[1]
BASH = (r'C:\Program Files\Git\bin\bash.exe' if os.name == 'nt' else shutil.which('bash'))


def script(path):
    data = (ROOT / path).read_text(encoding='utf-8')
    lines = data.split('        run: |\n', 1)[1].splitlines()
    result = []
    for line in lines:
        if line and not line.startswith('          '):
            break
        result.append(line[10:] if line else '')
    return '\n'.join(result)


class PolicyShellTests(unittest.TestCase):
    def run_script(self, file, **env):
        with tempfile.TemporaryDirectory() as directory:
            fake = Path(directory)
            gh = fake / 'gh'
            gh.write_text('''#!/usr/bin/env bash
echo "GH $*" >&2
if [[ "$*" == *'issues?state=all'* ]]; then printf '%s\\n' 12 13; exit 0; fi
if [[ "$*" == *'/labels?per_page=100'* ]]; then
  [[ "${MOCK_API_FAIL:-0}" == 1 ]] && exit 1
  if [[ -n "${MOCK_LABEL_AFTER:-}" ]]; then
    d="$(cd "$(dirname "$0")" && pwd)"; n=0; [[ -f "$d/count" ]] && n="$(cat "$d/count")"; n=$((n+1)); echo "$n" > "$d/count"
    if (( n >= MOCK_LABEL_AFTER )); then echo true; else echo false; fi
  else printf '%s\\n' "${MOCK_LABEL:-false}"; fi
  exit 0
fi
if [[ "$*" == *'/labels/launch'* ]]; then exit "${MOCK_LABEL_LOOKUP:-0}"; fi
if [[ "$1" == label && "$2" == create ]]; then exit "${MOCK_CREATE:-0}"; fi
exit 0
''', encoding='utf-8')
            (fake / 'sleep').write_text('#!/usr/bin/env bash\nexit 0\n', encoding='utf-8')
            gh.chmod(0o755)
            (fake / 'sleep').chmod(0o755)
            environ = dict(os.environ, PATH=str(fake) + os.pathsep + os.environ['PATH'],
                           GITHUB_REPOSITORY='Owner/Repo', **env)
            # Preserve the loop and all branches; omit real waiting in local tests.
            shell = script(file).replace('sleep 5', ':')
            return subprocess.run([BASH, '--noprofile', '--norc', '-c', shell],
                                  env=environ, capture_output=True, text=True, timeout=15)

    def test_owner_event_adds_only_numeric_item(self):
        r = self.run_script('.github/workflows/owner-launch-label.yml', EVENT_KIND='issues', OWNER_ID='42', ISSUE_NUMBER='17')
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn('issues/17/labels', r.stderr)
        self.assertNotIn('issues/18/labels', r.stderr)

    def test_malformed_issue_rejected_before_writes(self):
        r = self.run_script('.github/workflows/owner-launch-label.yml', EVENT_KIND='issues', OWNER_ID='42', ISSUE_NUMBER='17;echo x')
        self.assertNotEqual(r.returncode, 0)
        self.assertNotIn('GH ', r.stderr)

    def test_unknown_event_and_owner_rejected(self):
        for event, owner in [('other', '42'), ('issues', '42;bad')]:
            r = self.run_script('.github/workflows/owner-launch-label.yml', EVENT_KIND=event, OWNER_ID=owner, ISSUE_NUMBER='17')
            self.assertNotEqual(r.returncode, 0)
            self.assertNotIn('GH ', r.stderr)

    def test_create_failure_is_not_silently_ignored(self):
        r = self.run_script('.github/workflows/owner-launch-label.yml', EVENT_KIND='issues', OWNER_ID='42', ISSUE_NUMBER='17', MOCK_LABEL_LOOKUP='1', MOCK_CREATE='1')
        self.assertNotEqual(r.returncode, 0)
        self.assertNotIn('issues/17/labels', r.stderr)

    def test_label_is_created_when_missing(self):
        r = self.run_script('.github/workflows/owner-launch-label.yml', EVENT_KIND='issues', OWNER_ID='42', ISSUE_NUMBER='17', MOCK_LABEL_LOOKUP='1', MOCK_CREATE='0')
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn('GH label create launch', r.stderr)
        self.assertIn('issues/17/labels', r.stderr)

    def test_backfill_uses_owner_id_and_pages(self):
        r = self.run_script('.github/workflows/owner-launch-label.yml', EVENT_KIND='workflow_dispatch', GITHUB_REF='refs/heads/main', OWNER_ID='42', ISSUE_NUMBER='')
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn('--paginate', r.stderr)
        self.assertIn('select(.user.id == 42)', r.stderr)
        self.assertIn('issues/12/labels', r.stderr)
        self.assertIn('issues/13/labels', r.stderr)

    def test_backfill_rejects_nonmain(self):
        r = self.run_script('.github/workflows/owner-launch-label.yml', EVENT_KIND='workflow_dispatch', GITHUB_REF='refs/heads/untrusted', OWNER_ID='42', ISSUE_NUMBER='')
        self.assertNotEqual(r.returncode, 0)
        self.assertNotIn('GH ', r.stderr)

    def test_new_workflow_push_backfills_only_on_main(self):
        r = self.run_script('.github/workflows/owner-launch-label.yml', EVENT_KIND='push', GITHUB_REF='refs/heads/main', OWNER_ID='42', ISSUE_NUMBER='')
        self.assertEqual(r.returncode, 0, r.stderr)
        self.assertIn('select(.user.id == 42)', r.stderr)
        r = self.run_script('.github/workflows/owner-launch-label.yml', EVENT_KIND='push', GITHUB_REF='refs/heads/untrusted', OWNER_ID='42', ISSUE_NUMBER='')
        self.assertNotEqual(r.returncode, 0)
        self.assertNotIn('GH ', r.stderr)



if __name__ == "__main__":
    unittest.main()
