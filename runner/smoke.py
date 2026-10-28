"""Smoke test of the runner image through the Lambda runtime emulator.

Usage: python3 runner/smoke.py SITE_DIR [URL]
Sends API Gateway events for a few tasks: the reference solution must pass,
the starter must fail. The container runs with a read-only root and a /tmp
tmpfs, like Lambda.
"""
import json
import pathlib
import sys
import urllib.request

site = pathlib.Path(sys.argv[1])
url = sys.argv[2] if len(sys.argv) > 2 else "http://localhost:9000/2015-03-31/functions/function/invocations"
cases = [
    ("p-money", site / "docs/verification/fixtures/practice/money"),
    ("p-workerpool", site / "docs/verification/fixtures/practice/workerpool"),
    ("p-parallelfetch", site / "docs/verification/fixtures/practice-errgroup/parallelfetch"),
]


def run(task_id, code):
    event = {
        "version": "2.0",
        "routeKey": "POST /runs",
        "rawPath": "/runs",
        "body": json.dumps({"taskId": task_id, "code": code}),
        "isBase64Encoded": False,
        "requestContext": {"authorizer": {"jwt": {"claims": {"sub": "smoke"}}}, "http": {"method": "POST", "path": "/runs"}},
    }
    req = urllib.request.Request(url, data=json.dumps(event).encode(), headers={"content-type": "application/json"})
    with urllib.request.urlopen(req, timeout=120) as resp:
        out = json.load(resp)
    if out.get("statusCode") != 200:
        raise SystemExit(f"{task_id}: status {out.get('statusCode')}: {out.get('body')}")
    return json.loads(out["body"])


failed = False
for task_id, folder in cases:
    pkg = folder.name
    good = run(task_id, (folder / f"{pkg}.go").read_text())
    bad = run(task_id, (folder / "starter.go.txt").read_text())
    ok = good["passed"] and not bad["passed"]
    failed |= not ok
    print(f"{task_id}: solution passed={good['passed']} ({good['millis']} ms), starter passed={bad['passed']}")
    if not good["passed"]:
        print(good.get("build", ""), good.get("output", "")[-3000:])
sys.exit(1 if failed else 0)
