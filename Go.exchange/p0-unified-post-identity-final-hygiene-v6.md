# P0 — Unified Post Identity Final Hygiene

## Direct Agent Execution Spec v6

Status: ready for direct implementation

Repository root: `D:\code\mf`

Backend: `D:\code\mf\Go.exchange`

Audited source HEAD: `76e5e6aa30a00ef724aa31bc35a1365261747718`

This is the final narrow delta after v5. The functional Post identity implementation is accepted. Do not redesign or rewrite it.

---

# 0. Operating contract

Use the current dirty working tree as the baseline. Preserve every accepted v5 change.

Do not:

```text
change runtime behavior or API contracts
change .gitignore
restore, remove, or reorganize user-owned docs
change ranking, Kafka, outbox, Like, cache, or visibility semantics
add compatibility routes or Article/Comment identity adapters
stage, commit, push, reset, restore, or clean
remove pre-existing user files
```

This delta contains only:

```text
integration-test cleanup
CI proof that three release-blocking tests actually PASS
mechanical source-file renames
```

Before editing, record HEAD and `git status --short`. Verify every rename destination is absent.

---

# 1. Fix durable integration-fixture cleanup

`recommendation.InvalidateProfiles` writes durable rows to `user_reco_profile_dirty`. The new reply and Like-projection tests currently clean Posts, reactions, behaviors, inbox, outbox, users, and Redis, but can leave these durable queue rows behind.

Add exact fixture cleanup in all three locations:

## 1.1 `controllers/post_reply_integration_test.go`

In the shared `newReplyIntegrationFixture` cleanup, delete `models.UserRecoProfileDirty` rows for:

```text
fixture.Author.ID
fixture.Commenter.ID
fixture.Other.ID
```

Delete them before deleting the fixture users. This central cleanup covers every reply integration test using the fixture.

## 1.2 `controllers/post_identity_e2e_integration_test.go`

In `TestCanonicalPostDeletePGRedisIdentityE2E` cleanup, delete `models.UserRecoProfileDirty` for the exact `userIDs` fixture slice before deleting users.

Do not delete the whole queue.

## 1.3 `tasks/like_post_projection_integration_test.go`

In `TestCanonicalPostLikeRedisToPostgresProjectionIntegration` cleanup, delete `models.UserRecoProfileDirty` for the exact actor/author IDs before deleting those users.

Register cleanup immediately after swapping `global.Db`, `global.RedisDB`, and `config.AppConfig`, before creating fixture rows. Use zero-ID guards so a setup failure still restores globals and closes Redis safely.

Retain cleanup for:

```text
Redis aggregate and behavior keys
ConsumerInbox rows for the unique groups
PostReaction
PostBehavior
reaction activity OutboxEvent
Post
Users
```

Add a final assertion before cleanup, or a focused helper assertion, proving the actor's dirty row exists after the real behavior projection. This makes the cleanup target part of the tested contract rather than an unexplained deletion.

Gate:

```text
each test removes only its own durable dirty rows
global state is restored even when fixture creation fails
no test flushes a shared table, Redis DB, or queue
```

---

# 2. Require actual PASS for all release-blocking integration tests

Update the existing PostgreSQL/Redis integration step in `.github/workflows/ci.yml`.

Replace the single name-presence check with a loop that requires a `--- PASS:` log marker for all three tests:

```text
TestCanonicalPostDeletePGRedisIdentityE2E
TestCanonicalPostLikeRedisToPostgresProjectionIntegration
TestCanonicalReplyInvalidatesParentDetailCacheWithoutTTLIntegration
```

Required shell behavior:

```bash
for required_test in \
  TestCanonicalPostDeletePGRedisIdentityE2E \
  TestCanonicalPostLikeRedisToPostgresProjectionIntegration \
  TestCanonicalReplyInvalidatesParentDetailCacheWithoutTTLIntegration
do
  if ! grep -Fq -- "--- PASS: ${required_test}" "$RUNNER_TEMP/go-integration.log"; then
    echo "Required integration test did not pass: ${required_test}"
    exit 1
  fi
done
```

Keep:

```text
go test -v ./... -count=1 -p 1
test process exit-code handling
existing PostgreSQL and Redis missing-environment skip guards
```

A discovered or skipped test is not enough for CI acceptance; these three must emit PASS.

Gate:

```text
build tags cannot silently remove any of the three tests
an unexpected skip cannot produce a green integration job
```

---

# 3. Finish generic Post path naming

These files contain generic Post identity, not true long-form PostArticle-only concepts. Rename them mechanically without changing contents.

## 3.1 Models

```text
models/article.go                    -> models/post.go
models/article_behavior.go           -> models/post_behavior.go
models/article_embedding.go          -> models/post_embedding.go
models/article_reaction.go           -> models/post_reaction.go
models/article_repost.go             -> models/post_repost.go
models/user_article_reco_state.go    -> models/user_post_reco_state.go
```

## 3.2 Embedding helpers

```text
embeddings/article_text.go           -> embeddings/post_text.go
embeddings/article_text_test.go      -> embeddings/post_text_test.go
```

## 3.3 Worker implementation/tests

```text
tasks/article_embedding.go                    -> tasks/post_embedding.go
tasks/article_embedding_consumer_test.go      -> tasks/post_embedding_consumer_test.go
tasks/article_embedding_integration_test.go   -> tasks/post_embedding_integration_test.go
```

## 3.4 Migration tests

```text
initialize/article_embedding_migration_integration_test.go
    -> initialize/post_embedding_migration_integration_test.go

initialize/article_engagement_migration_integration_test.go
    -> initialize/post_engagement_migration_integration_test.go

initialize/comment_migration_integration_test.go
    -> initialize/post_graph_migration_integration_test.go
```

Do not rename true long-form concepts in this delta. These remain valid:

```text
models/post_article.go
consts/article.go containing PostArticle publication state
article-cover assets/upload/backfill
frontend ArticleCreateView, articleDraft, and articleReadTracker
PostArticle type/field/helper names
test fixture names that explicitly create PostArticle rows
```

Do not perform broad symbol renames. This phase is path-only.

Gate:

```text
each old source path is absent
each new destination exists
file contents are unchanged by the move
Go package behavior is unchanged
```

---

# 4. Validation

From `D:\code\mf\Go.exchange`:

```powershell
gofmt -l <changed Go files>
go vet ./...
go test ./... -count=1
go test ./cmd/requeue-post-embeddings -count=1
git diff --check
```

The `gofmt -l` command must print nothing.

Without local PostgreSQL/Redis, run discovery explicitly and report SKIP:

```powershell
go test -v ./controllers ./tasks -run "TestCanonicalPostDeletePGRedisIdentityE2E|TestCanonicalPostLikeRedisToPostgresProjectionIntegration|TestCanonicalReplyInvalidatesParentDetailCacheWithoutTTLIntegration" -count=1
```

With disposable PostgreSQL and Redis, or in CI, run:

```powershell
go test -v ./... -count=1 -p 1
```

The three required tests must all show `PASS`, not `SKIP`.

No frontend production code changes are expected. Run only if frontend files changed or as the final monorepo regression gate:

```powershell
npm.cmd test
npm.cmd run build
```

If build-generated `src/**/*.js` files did not exist before the command, remove exactly those generated files after verifying their resolved paths remain inside `Exchangeapp_frontend\src`.

Completion path search:

```powershell
rg --files models embeddings tasks initialize | Select-String "article|comment"
```

Classify every remaining hit. Only the explicitly allowed true PostArticle cases above may remain.

---

# 5. Definition of Done

Complete only when:

```text
all three integration fixtures remove their exact UserRecoProfileDirty rows
the combined Like test restores globals/resources on early setup failure
CI requires PASS from graph E2E, Redis-to-PostgreSQL Like projection, and real cache invalidation
generic model, embedding, worker, and migration paths use Post naming
all available Go/static/frontend gates pass
PostgreSQL/Redis execution is reported truthfully
.gitignore and user-owned documentation are untouched
```

Final report must include the exact three integration PASS/SKIP results and final `git status --short`.
