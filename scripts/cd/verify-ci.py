#!/usr/bin/env python3
"""Fail closed on the latest existing ci.yml run for the exact dispatch SHA."""
import json
import os
import re
import urllib.request

REQUIRED = {
    "Backend / Unit and Static": ["Go vet", "Backend baseline tests"],
    "Backend / PostgreSQL and Redis Integration": [
        "Configure PostgreSQL logical replication", "Run first migration",
        "Run second migration", "Run integration tests"],
    "Kafka / Topic Bootstrap": ["Provision topics first time", "Provision topics idempotently",
                                "Assert broker exposes all provisioned topics"],
    "Containers / Build": ["Build backend image", "Validate production Compose and maintenance scripts",
                           "Build frontend image"],
}


def validate(run, jobs, sha, workflow_id):
    assert run["head_sha"] == sha and run["head_branch"] == "main", "CI SHA/branch mismatch"
    assert run["workflow_id"] == workflow_id, "Wrong CI workflow"
    assert run["event"] in ("push", "workflow_dispatch"), "PR merge CI is insufficient"
    assert run["status"] == "completed" and run["conclusion"] == "success", "Latest CI is not successful"
    for name, steps in REQUIRED.items():
        matches = [j for j in jobs if j["name"] == name]
        assert len(matches) == 1, "Required CI job absent/duplicated: " + name
        job = matches[0]
        assert job["status"] == "completed" and job["conclusion"] == "success", "CI job failed/skipped: " + name
        for step in steps:
            matches = [s for s in job["steps"] if s["name"] == step]
            assert len(matches) == 1 and matches[0]["conclusion"] == "success", "Required CI step not passed: " + step


def main():
    sha = os.environ["TARGET_SHA"]
    assert re.fullmatch(r"[0-9a-f]{40}", sha), "Full SHA required"
    assert os.environ["GITHUB_REF"] == "refs/heads/main", "main only"
    assert os.environ["GITHUB_SHA"] == sha, "Dispatch SHA differs from expected SHA"
    repo = os.environ["GITHUB_REPOSITORY"]
    assert repo == "cc483858939-ops/exchange-platform", "Wrong repository"

    def api(path):
        req = urllib.request.Request("https://api.github.com/repos/" + repo + path, headers={
            "Authorization": "Bearer " + os.environ["GH_TOKEN"],
            "Accept": "application/vnd.github+json", "X-GitHub-Api-Version": "2022-11-28"})
        with urllib.request.urlopen(req, timeout=30) as response:
            return json.load(response)

    assert api("/git/ref/heads/main")["object"]["sha"] == sha, "main advanced; dispatch a new check"
    workflow = api("/actions/workflows/ci.yml")
    assert workflow["path"] == ".github/workflows/ci.yml" and workflow["state"] == "active"
    runs = api("/actions/workflows/ci.yml/runs?branch=main&head_sha=" + sha + "&per_page=100")["workflow_runs"]
    runs = [r for r in runs if r["event"] in ("push", "workflow_dispatch")]
    assert runs, "No CI run exists for target SHA"
    run = max(runs, key=lambda r: r["id"])
    # This endpoint selects the latest attempt, including jobs skipped on a rerun.
    jobs = []
    page = 1
    while True:
        batch = api(f'/actions/runs/{run["id"]}/jobs?filter=latest&per_page=100&page={page}')["jobs"]
        jobs.extend(batch)
        if len(batch) < 100:
            break
        page += 1
    validate(run, jobs, sha, workflow["id"])
    # A final read catches main advancing while CI was inspected.
    assert api("/git/ref/heads/main")["object"]["sha"] == sha, "main advanced during CI verification"
    print(json.dumps({"ci": "success", "sha": sha, "run_id": run["id"], "attempt": run["run_attempt"]}))


if __name__ == "__main__":
    main()
