#!/usr/bin/env python3
"""Assert that deployed Worker configurations omit API-only dependencies."""

from __future__ import annotations

import json
import subprocess
from pathlib import Path


ROOT = Path(__file__).resolve().parents[2]
FORBIDDEN_ENV_PREFIXES = ("MINIO_", "JWT_")
FORBIDDEN_WORKER_CONSTRUCTORS = (
    "config.NewStorageClient(",
    "global.MinioClient",
    "config.OpenAPIDatabase(",
    "auth.LoadConfigFromEnv(",
    "auth.NewManager(",
    "auth.NewRedisRefreshStore(",
    "core.StartHttpServer(",
    "JWT_PRIVATE_KEY_FILE",
    "JWT_VERIFY_KEYS_DIR",
    "JWT_ACTIVE_KID",
)
POST_MEDIA_GC_ENVIRONMENT = {
    "POST_MEDIA_GC_DATABASE_DSN",
    "POST_MEDIA_GC_MINIO_ENDPOINT",
    "POST_MEDIA_GC_MINIO_ACCESS_KEY",
    "POST_MEDIA_GC_MINIO_SECRET_KEY",
    "POST_MEDIA_GC_MINIO_BUCKET",
    "POST_MEDIA_GC_MINIO_USE_SSL",
    "POST_MEDIA_GC_BATCH_SIZE",
    "POST_MEDIA_GC_MAX_ROWS_PER_RUN",
    "POST_MEDIA_GC_CLAIM_TIMEOUT",
    "POST_MEDIA_GC_RETRY_BASE",
    "POST_MEDIA_GC_RETRY_MAX",
    "POST_MEDIA_GC_OBJECT_TIMEOUT",
    "POST_MEDIA_GC_RUN_TIMEOUT",
}


def compose_config(*arguments: str) -> dict:
    result = subprocess.run(
        ["docker", "compose", *arguments, "config", "--format", "json"],
        cwd=ROOT,
        check=False,
        capture_output=True,
        text=True,
    )
    if result.returncode != 0:
        raise SystemExit(
            f"docker compose config failed for {arguments}:\n{result.stderr}"
        )
    try:
        return json.loads(result.stdout)
    except json.JSONDecodeError as error:
        raise SystemExit(f"docker compose returned invalid JSON: {error}") from error


def assert_worker_isolated(compose: dict, label: str) -> dict:
    worker = compose.get("services", {}).get("worker")
    if not isinstance(worker, dict):
        raise SystemExit(f"{label}: worker service is missing")

    environment = worker.get("environment") or {}
    forbidden_environment = sorted(
        name
        for name in environment
        if name.startswith(FORBIDDEN_ENV_PREFIXES)
    )
    if forbidden_environment:
        raise SystemExit(
            f"{label}: worker receives API-only environment keys: "
            + ", ".join(forbidden_environment)
        )

    dependencies = worker.get("depends_on") or {}
    dependency_names = (
        set(dependencies) if isinstance(dependencies, dict) else set(dependencies)
    )
    if "minio" in dependency_names:
        raise SystemExit(f"{label}: worker depends on MinIO")

    for mount in worker.get("volumes") or []:
        rendered_mount = json.dumps(mount, sort_keys=True).lower()
        if "jwt" in rendered_mount or "/run/secrets" in rendered_mount:
            raise SystemExit(f"{label}: worker mounts API/JWT secrets: {mount}")

    return worker


def assert_post_media_gc_isolated(compose: dict) -> dict:
    service = compose.get("services", {}).get("post-media-gc")
    if not isinstance(service, dict):
        raise SystemExit("production Compose: post-media-gc service is missing")
    if service.get("profiles") != ["maintenance"]:
        raise SystemExit("production Compose: post-media-gc must be maintenance-profile only")
    if service.get("entrypoint") != ["/app/go-exchange-post-media-gc"]:
        raise SystemExit("production Compose: post-media-gc must run its dedicated binary")

    environment = service.get("environment") or {}
    unexpected_environment = sorted(set(environment) - POST_MEDIA_GC_ENVIRONMENT)
    if unexpected_environment:
        raise SystemExit(
            "production Compose: post-media-gc received unrelated environment keys: "
            + ", ".join(unexpected_environment)
        )
    if set(environment) != POST_MEDIA_GC_ENVIRONMENT:
        missing_environment = sorted(POST_MEDIA_GC_ENVIRONMENT - set(environment))
        raise SystemExit(
            "production Compose: post-media-gc is missing dedicated configuration keys: "
            + ", ".join(missing_environment)
        )

    dependencies = service.get("depends_on") or {}
    dependency_names = set(dependencies) if isinstance(dependencies, dict) else set(dependencies)
    if dependency_names != {"db", "migrate", "minio"}:
        raise SystemExit(
            "production Compose: post-media-gc may depend only on db, migrate, and minio"
        )
    return service


def assert_kubernetes_post_media_gc_isolated() -> None:
    cronjob = (ROOT / "Go.exchange/k8s/post-media-gc-cronjob.yaml").read_text(
        encoding="utf-8"
    )
    required_contract = (
        "kind: CronJob",
        'schedule: "*/10 * * * *"',
        "concurrencyPolicy: Forbid",
        "activeDeadlineSeconds: 540",
        "command: [\"/app/go-exchange-post-media-gc\"]",
        "POST_MEDIA_GC_DATABASE_DSN",
        "POST_MEDIA_GC_MINIO_ACCESS_KEY",
        "POST_MEDIA_GC_MINIO_SECRET_KEY",
        "name: go-exchange-post-media-gc-secrets",
        "restartPolicy: Never",
    )
    for required in required_contract:
        if required not in cronjob:
            raise SystemExit(f"Kubernetes Post media GC CronJob is missing {required}")
    if "envFrom:" in cronjob:
        raise SystemExit("Kubernetes Post media GC CronJob must use explicit environment keys")
    for forbidden in ("REDIS", "KAFKA", "JWT", "TWITTER", "go-exchange-runtime-secrets"):
        if forbidden in cronjob:
            raise SystemExit(
                f"Kubernetes Post media GC CronJob must not receive {forbidden} configuration"
            )

    gc_sources = sorted((ROOT / "Go.exchange/postmediagc").glob("*.go"))
    if not gc_sources:
        raise SystemExit("Post media GC implementation sources are missing")
    if any("ListObjects" in source.read_text(encoding="utf-8") for source in gc_sources):
        raise SystemExit("Post media GC must delete row-owned keys without scanning MinIO")


def main() -> None:
    dev = compose_config("-f", "docker-compose.yml")
    dev_worker = assert_worker_isolated(dev, "development Compose")
    dev_command = dev_worker.get("command") or []
    if not isinstance(dev_command, list) or dev_command[-2:] != [
        "run",
        "./cmd/worker",
    ]:
        raise SystemExit(
            "development Compose worker must run the dedicated ./cmd/worker target"
        )

    prod = compose_config(
        "--profile",
        "maintenance",
        "--env-file",
        "deploy/.env.example",
        "-f",
        "deploy/compose.prod.yml",
    )
    prod_worker = assert_worker_isolated(prod, "production Compose")
    if prod_worker.get("entrypoint") != ["/app/go-exchange-worker"]:
        raise SystemExit(
            "production Compose worker entrypoint must be /app/go-exchange-worker"
        )
    assert_post_media_gc_isolated(prod)

    worker_source = (ROOT / "Go.exchange/cmd/worker/main.go").read_text(
        encoding="utf-8"
    )
    for forbidden in FORBIDDEN_WORKER_CONSTRUCTORS:
        if forbidden in worker_source:
            raise SystemExit(
                f"Worker bootstrap must not reference API-only dependency {forbidden}"
            )

    kubernetes_worker = (
        ROOT / "Go.exchange/k8s/worker-deployment.yaml"
    ).read_text(encoding="utf-8")
    if 'command: ["/app/go-exchange-worker"]' not in kubernetes_worker:
        raise SystemExit(
            "Kubernetes worker must execute /app/go-exchange-worker"
        )
    for forbidden in FORBIDDEN_ENV_PREFIXES:
        if forbidden in kubernetes_worker:
            raise SystemExit(
                f"Kubernetes worker must not declare {forbidden} configuration"
            )
    if "volumeMounts:" in kubernetes_worker and "jwt" in kubernetes_worker.lower():
        raise SystemExit("Kubernetes worker must not mount JWT secrets")

    assert_kubernetes_post_media_gc_isolated()

    print(
        "Worker isolation and dedicated Post media GC runtime assertions passed "
        "for Compose and Kubernetes."
    )


if __name__ == "__main__":
    main()
