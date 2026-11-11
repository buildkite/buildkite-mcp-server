#!/usr/bin/env python3
"""Generate synthetic API replay snapshots, not recordings of production builds."""

import json
from pathlib import Path
from urllib.parse import urlencode


def generate():
    entries = []
    timestamp = "2026-11-11T01:00:00.000000Z"

    def record(path, body, query=None, mime="application/json", method="GET"):
        text = json.dumps(body, separators=(",", ":")) if mime == "application/json" else body
        url = "https://api.buildkite.com/v2/" + path
        if query:
            url += "?" + urlencode(sorted(query.items()), doseq=True)
        entry = {
            "request": {"method": method, "url": url},
            "response": {
                "status": 200, "statusText": "OK", "headers": [],
                "content": {"mimeType": mime, "text": text, "size": len(text.encode())},
            },
        }
        # Each call consumes an entry. Permit bounded repeated inspection of the same snapshot.
        entries.extend([entry] * 5)

    scenarios = [
        ("1", "Typecheck", "src/invoice.ts:17: Type 'string' is not assignable to type 'number'.", True),
        ("2", "Install", "npm ERR! E404: @acme/missing-lib@9.9.9 is not in this registry.", False),
    ]
    for number, name, failure, annotated in scenarios:
        build_id = f"00000000-0000-4000-8000-{int(number):012d}"
        job_id = f"10000000-0000-4000-8000-{int(number):012d}"
        path = f"organizations/evidence-eval/pipelines/fixture/builds/{number}"
        web_url = f"https://buildkite.com/evidence-eval/fixture/builds/{number}"
        build = {"id": build_id, "number": int(number), "state": "failed", "web_url": web_url}
        job = {
            "id": job_id, "name": name, "type": "script", "step_key": name.lower(),
            "state": "failed", "exit_status": 1, "soft_failed": False,
            "started_at": timestamp, "finished_at": "2026-11-11T01:01:00.000000Z",
        }
        annotations = [{
            "id": f"20000000-0000-4000-8000-{int(number):012d}", "job_id": None,
            "scope": "build", "context": "typecheck", "style": "error", "body": failure,
            "body_truncated": False,
        }] if annotated else []
        evidence_job = {**job, "state": "finished", "annotations": [], "annotations_truncated": False,
                        "web_url": f"{web_url}#{job_id}", "collected_at": timestamp,
                        "test_results": {"status": "not_requested", "collected_at": timestamp},
                        "omissions": {"logs": "not_collected"}}
        evidence_job.pop("type")
        evidence_job.pop("soft_failed")
        record(path + "/failure-evidence", {
            "schema_version": 1, "build": build, "jobs": [evidence_job], "job_limit": 10,
            "jobs_truncated": False, "annotations": annotations, "annotations_truncated": False,
            "collected_at": timestamp,
        }, {"job_limit": 10})
        record(path, {**build, "job_state_counts": {"total": 1, "states": {"failed": 1}}},
               {"exclude_jobs": "true", "exclude_pipeline": "true", "include_test_engine": "true"})
        record(path + "/jobs", {"items": [job], "links": {}}, {
            "include_retried_jobs": "false", "per_page": 11,
            "state[]": ["failed", "timed_out", "expired"],
        })
        for states in [["canceled"], ["waiting_failed", "blocked_failed", "unblocked_failed"], ["broken"]]:
            record(path + "/jobs", {"items": [], "links": {}}, {
                "include_retried_jobs": "false", "per_page": 10, "state[]": states,
            })
        record(path + "/annotations", annotations,
               {"page": 1, "per_page": 100, "scope": "all", "body_format": "raw"})
        record(path + f"/jobs/{job_id}", job)
        log = "".join(f"Checking dependency module {i}: no error detected\n" for i in range(60))
        record(path + f"/jobs/{job_id}/log", "", mime="text/plain", method="HEAD")
        record(path + f"/jobs/{job_id}/log", log + failure + "\nProcess exited with status 1\n", mime="text/plain")
    return {"log": {"version": "1.2", "creator": {"name": "synthetic-failure-evidence-eval", "version": "1"},
                    "entries": entries}}


if __name__ == "__main__":
    output = Path(__file__).resolve().parents[1] / "fixtures" / "failure-evidence.har"
    output.parent.mkdir(exist_ok=True)
    output.write_text(json.dumps(generate(), indent=2) + "\n")
