# P0 — Unified Post Identity Remaining Closure

## Direct Agent Execution Spec v5

Status: ready for direct implementation

Repository root: `D:\code\mf`

Backend: `D:\code\mf\Go.exchange`

Frontend: `D:\code\mf\Exchangeapp_frontend`

Audited source HEAD:

```text
76e5e6aa30a00ef724aa31bc35a1365261747718
```

This is a narrow corrective delta for the partially implemented Unified Post identity cutover. It does not replace the main Post architecture contract. Apply only the remaining work specified here and preserve the already-correct implementation.

The repository is intentionally dirty. Treat the current working tree as the implementation baseline.

---

# 0. Operating contract

Before editing:

1. Verify repository root, HEAD, and `git status --short`.
2. Read every currently modified/untracked file before changing overlapping code.
3. Preserve all unrelated and user-owned changes.
4. Record the initial dirty-path set so generated artifacts can be distinguished from user work.

Do not:

```text
reset, restore, checkout, clean, stage, commit, or push
change .gitignore
restore, remove, or reorganize user-owned documentation
resurrect Article or Comment compatibility APIs
add an ID translation layer or dual writes
redesign ranking, Kafka, outbox, cache, or Like semantics
weaken or delete existing tests to obtain a green result
claim PostgreSQL, Redis, Kafka, Docker, browser, CI, or deployment acceptance when it was not run
```

Use `apply_patch` for content edits. Renames may be done as explicit, one-file-at-a-time moves after verifying every source and destination path. Format only changed Go files.

If current code contradicts this spec, stop and report the contradiction. Do not silently invent a new contract.

---

# 1. Accepted partial implementation — do not redo

The following work is accepted and must remain intact:

```text
Post.ID is the canonical content identity.
POST /api/posts is the only create path.
DELETE /api/posts/:id is the only delete path.
GET /api/posts/:id/replies is the direct-reply read path.
CreatePostReply, DeletePostReply, createReplyWithCount, and deleteReplyWithCount are absent.
Canonical reply creation and deletion update direct-parent reply_count transactionally.
RowsAffected != 1 is treated as a consistency failure and rolls back.
Reply-to-reply conversation_id normalization uses the effective root.
Create and delete invalidate the direct-parent detail cache after commit, best effort.
Deleting a reply under a tombstoned parent decrements the tombstoned parent's count.
Create-versus-parent-delete serialization is covered.
PostReference is the deleted:true/deleted:false discriminated contract.
Deleted or unavailable references serialize exactly as {id, deleted:true}.
Active references expose only the bounded active-reference fields.
Frontend PostCard and PostDetail consume the deleted discriminant.
Redis Like purge removes only target Post aggregate/claim keys.
Behavior relay keys and relational PostReaction projection are not deleted by purge.
Canonical route tests no longer accept shadow Article/Comment mutations.
```

Do not introduce a second mutation service while completing the remaining tests.

---

# 2. Remaining release blockers

Completion is blocked until all of these are resolved:

| ID | Problem | Required outcome |
| --- | --- | --- |
| R1 | Bounded-reference loading adds `posts.created_at <= now`, which is not part of shared public eligibility | Reference and normal detail use the same eligibility rules |
| R2 | The old reply 1000-Unicode-character validation and request-size protection disappeared during route consolidation | Canonical `POST /api/posts` preserves explicit, tested input bounds |
| R3 | `TestCanonicalPostDeletePGRedisIdentityE2E` has an `integration` build tag, while CI runs without that tag | The required scenario is compiled and executed by the existing integration job |
| R4 | The final scenario omits root-storage/API normalization for P4 and does not prove the real Redis-to-PostgreSQL Like projection boundary for P2 | Both graph and asynchronous Like consistency are proven without fake substitute rows |
| R5 | The real Redis cache test proves reply creation invalidation but not reply deletion invalidation | Cached parent detail reloads with count zero immediately after canonical delete |

Structural closure is also required: generic Post/reply source files and the Post embedding reconciliation command must not retain legacy Article/Comment names.

---

# 3. Phase A — restore one shared reference eligibility rule

## 3.1 Production change

In `controllers/article_loader.go` (renamed later in Phase F), change the active reference lookup to apply the existing `publicPostScope` to the target ID only.

Required semantic form:

```go
publicPostScope(
    preloadPostAuthor(db.Model(&models.Post{})).Where("posts.id = ?", *id),
    now,
)
```

Remove this reference-only predicate:

```sql
posts.created_at <= now
```

Do not add that predicate to `publicPostScope` or normal detail. The frozen rule is:

```text
normal non-article Post:
    active Post
    visibility=public
    active author
    Post.created_at is its publish timestamp, but future created_at is not a separate visibility gate

PostArticle:
    all normal rules
    publication_state=published
    published_at non-null and <= now
    expired_at null or > now
```

The structural unscoped lookup remains. Missing, soft-deleted, or no-longer-public targets still return the exact tombstone reference.

## 3.2 Tests

Update the focused reference tests to prove:

```text
future-created normal Post -> active bounded reference
future-published PostArticle -> tombstone reference
expired PostArticle -> tombstone reference
soft-deleted Post -> tombstone reference
Post with inactive/deleted author -> tombstone reference
active normal/PostArticle reference -> exact active bounded keys
tombstone -> exactly id and deleted, with no author/content/article leakage
```

Do not create a separate eligibility helper for references.

Phase gate:

```text
one shared publicPostScope decides normal detail and active reference eligibility
the future normal-Post test no longer encodes a reference-only rule
```

---

# 4. Phase B — canonical create input protection

Implement validation on the one canonical create handler. Do not restore the removed reply handler.

## 4.1 Frozen limits

Define named constants next to the canonical create request handling:

```go
const (
    createPostRequestMaxBytes = 1 << 20 // 1 MiB JSON request envelope
    maxReplyContentRunes      = 1000
)
```

Use `http.MaxBytesReader` before JSON binding. Bind as JSON and preserve the status contract:

```text
request larger than 1 MiB -> 413
malformed JSON -> 400
empty/whitespace content -> 400
reply content of 1000 Unicode code points -> accepted
reply content of 1001 Unicode code points -> 400
```

Apply the 1000-rune semantic limit only when `reply_to_post_id` is present. Do not impose the old reply limit on a root Post, quote Post, or long-form PostArticle body.

All content remains trimmed before persistence. Count Unicode code points after trimming with `utf8.RuneCountInString`.

The 1 MiB envelope cap applies uniformly to `POST /api/posts`; it is intentionally large enough for the existing long-form JSON body while still bounding parser memory. It does not authorize inline media or binary payloads.

## 4.2 Tests

Add canonical handler tests proving:

```text
1000 Unicode-rune reply succeeds through the persistence seam
1001 Unicode-rune reply returns 400
root or PostArticle content over 1000 runes is not rejected by the reply-only rule
request body over 1 MiB returns 413
malformed JSON returns 400
```

For every rejected request, assert:

```text
persistPostGraphFn was not called
embedding publisher was not called
Redis Like initialization was not called
parent cache invalidation was not called
```

Do not require a database just to test these validation branches.

Phase gate:

```text
the semantic reply limit survives route consolidation
the canonical endpoint has an explicit transport bound
long-form content is not accidentally limited to 1000 runes
```

---

# 5. Phase C — make the required graph E2E part of CI

## 5.1 Remove the private build-tag island

From `controllers/post_identity_e2e_integration_test.go`, remove:

```go
//go:build integration
// +build integration
```

Use the repository's normal integration-test convention:

```go
if os.Getenv("POSTGRES_TEST_DSN") == "" {
    t.Skip("set POSTGRES_TEST_DSN to run PostgreSQL integration test")
}
if os.Getenv("REDIS_TEST_ADDR") == "" {
    t.Skip("set REDIS_TEST_ADDR to run Redis integration test")
}
```

Keep the guards separate so the missing dependency is explicit. Do not use `t.Fatal` merely because a developer runs the default local suite without integration services.

Do not add a second tagged CI command. The existing integration job already supplies both services and runs:

```text
go test -v ./... -count=1 -p 1
```

## 5.2 CI presence assertion

In the existing integration test shell step in `.github/workflows/ci.yml`, after the test process finishes and before success, require the verbose log to contain the run marker for:

```text
TestCanonicalPostDeletePGRedisIdentityE2E
```

If the marker is absent, print a clear message and exit non-zero. Preserve the existing missing-PostgreSQL and missing-Redis skip-message checks.

This assertion prevents a future build tag or filename/package mistake from silently excluding the release-blocking scenario.

## 5.3 Complete graph assertions

Keep the current P1/P2/P3/P4 scenario and add the missing P4 checks.

Immediately after P4 creation, query its stored `models.Post` row and assert:

```text
reply_to_post_id = NULL
quote_post_id = P1
conversation_id = NULL
deleted_at = NULL
```

From the canonical API response, assert:

```text
P4 conversation_id = P4
P4 quote_post_id = P1
P4 quote_post is active before P1 deletion
```

After P1 deletion, retain the exact tombstone and no-content/no-author-leak assertions.

## 5.4 Do not fake P2 Like projection

The controller E2E may retain a deliberately created P1 `PostReaction` only when its assertion is clearly named as the delete contract: soft-deleting P1 and purging Redis must not delete the relational projection row.

That P1 row is not evidence that P2's Like pipeline is consistent.

For P2 in the controller scenario, assert the synchronous authoritative state that exists at request completion:

```text
Redis ready/count/users/version represent B liking P2 once
the P2 snapshot dirty marker exists before relay
the P2 behavior state/dirty marker exists before relay
```

Then add one combined PostgreSQL+Redis integration test under package `tasks`, where the real unexported pipeline seams are already available. Suggested name:

```go
TestCanonicalPostLikeRedisToPostgresProjectionIntegration
```

The test must:

1. Skip independently with the repository's exact PostgreSQL and Redis skip messages.
2. Create real User and Post rows.
3. Initialize a real `likes.Store` and execute one real Like mutation.
4. Assert Redis count/user/version plus snapshot and behavior dirty state.
5. Run `runLikeSnapshotRelayBatch` with a capture publisher.
6. Apply the captured snapshot using `applyLikeSnapshotEvent`.
7. Run `runLikeBehaviorRelayBatch` with a capture publisher.
8. Apply the captured behavior envelope using `applyUserBehaviorEvent`.
9. Assert the Post row has `like_count=1` and the matching `like_sync_version`.
10. Assert `PostReaction(user=B, post=P2)` is `liked=true` with the same Like version.
11. Replay both captured events and prove inbox/version idempotency.
12. Clean up only the exact fixture rows and Redis keys created by the test.

Reuse the real store, relay, envelope, inbox, and projection functions. Do not directly insert the expected P2 `PostReaction` and do not update the expected P2 `Post.like_count` from the test.

Configure unique consumer group IDs and required activity topic values in the test, and restore `global.Db`, `global.RedisDB`, and `config.AppConfig` in cleanup.

Phase gate:

```text
default local go test discovers the graph E2E and skips truthfully when services are absent
CI integration executes it rather than merely compiling it
P4 storage/API normalization is frozen
P2 Like consistency crosses real Redis relay and PostgreSQL projection seams
```

---

# 6. Phase D — prove real delete cache invalidation

Extend or split `TestCanonicalReplyInvalidatesParentDetailCacheWithoutTTLIntegration`.

Required scenario against real PostgreSQL and Redis:

```text
create parent with reply_count=0
load parent detail once to prime the cache
create reply through canonical POST /api/posts
load parent detail and assert reply_count=1 without waiting for TTL
load again if needed to prove the count-one response is cached
delete the reply through canonical DELETE /api/posts/:reply_id
load parent detail immediately and assert reply_count=0 without waiting for TTL
```

The delete must call the production cache invalidation path; do not replace it with a stub in this test. Keep the existing focused best-effort/failure tests as separate transaction-boundary coverage.

Also assert the deleted reply's normal detail is 404 and the parent remains active.

Phase gate:

```text
both create and delete are proven through the real cache with no TTL wait
mock/stub assertions are not the only evidence for delete invalidation
```

---

# 7. Phase E — legacy operational naming closure

This phase is structural only. Do not change behavior while renaming.

All currently generic Post files whose names still imply Article identity must become Post-named files:

```text
controllers/article_author_validation.go              -> controllers/post_author_validation.go
controllers/article_behavior.go                       -> controllers/post_behavior.go
controllers/article_cache.go                          -> controllers/post_cache.go
controllers/article_cache_test.go                     -> controllers/post_cache_test.go
controllers/article_cache_visibility_test.go          -> controllers/post_cache_visibility_test.go
controllers/article_controller.go                     -> controllers/post_controller.go
controllers/article_controller_test.go                -> controllers/post_controller_test.go
controllers/article_cursor.go                         -> controllers/post_cursor.go
controllers/article_delete.go                         -> controllers/post_delete.go
controllers/article_delete_test.go                    -> controllers/post_delete_test.go
controllers/article_engagement_test.go                -> controllers/post_engagement_test.go
controllers/article_integration_test.go               -> controllers/post_integration_test.go
controllers/article_loader.go                         -> controllers/post_loader.go
controllers/article_reference_test.go                 -> controllers/post_reference_test.go
controllers/article_response.go                       -> controllers/post_response.go
controllers/article_timeline_sql_integration_test.go  -> controllers/post_timeline_sql_integration_test.go
controllers/article_view_event_controller.go          -> controllers/post_view_event_controller.go
controllers/article_view_event_controller_test.go     -> controllers/post_view_event_controller_test.go
```

All generic reply files must become Post-reply-named files:

```text
controllers/comment_controller.go                     -> controllers/post_reply_controller.go
controllers/comment_controller_test.go                -> controllers/post_reply_controller_test.go
controllers/comment_store.go                          -> controllers/post_reply_store.go
controllers/comment_response.go                       -> controllers/post_reply_response.go
controllers/comment_integration_test.go                -> controllers/post_reply_integration_test.go
controllers/comment_engagement_integration_test.go     -> controllers/post_reply_engagement_integration_test.go
controllers/comment_reply_integration_test.go          -> controllers/post_reply_signal_integration_test.go
```

Rename the already Post-based embedding command directory:

```text
cmd/requeue-article-embeddings -> cmd/requeue-post-embeddings
```

The command implementation already uses Post identity; do not rewrite it during the move.

Names that represent true long-form metadata/UI remain allowed, including:

```text
PostArticle
createPostArticleRequest
postArticleResponse
article title/preview/cover/expiry fields
article-cover upload and backfill command
ArticleCreate frontend UI
```

After the moves, search executable source paths and classify every remaining filename containing `article` or `comment`. A remaining generic operational identity is a failure; a true PostArticle/editor/cover occurrence is allowed.

Phase gate:

```text
Go package behavior is unchanged
generic content files are discoverable under Post/PostReply names
cmd/requeue-post-embeddings builds and tests
no compatibility symbol or route was introduced
```

---

# 8. Required validation

## 8.1 Static and source tests

From `D:\code\mf\Go.exchange`:

```powershell
gofmt -w <only changed Go files>
go vet ./...
go test ./... -count=1
go test ./cmd/requeue-post-embeddings -count=1
git diff --check
```

The untagged default Go test must discover the two service-backed tests and may report only the repository's explicit skip messages when local services are absent.

Compilation with a private tag is not acceptance.

## 8.2 PostgreSQL and Redis integration

With both disposable test services available:

```powershell
go test -v ./... -count=1 -p 1
```

The output must show execution, not skip, of at least:

```text
TestCanonicalPostDeletePGRedisIdentityE2E
TestCanonicalPostLikeRedisToPostgresProjectionIntegration
TestCanonicalReplyInvalidatesParentDetailCacheWithoutTTLIntegration
```

If either environment variable or service is unavailable, report this gate as `SKIPPED` or `BLOCKED`, never PASS.

## 8.3 Frontend regression

No frontend production change is expected in this delta. Still run from `D:\code\mf\Exchangeapp_frontend`:

```powershell
npm.cmd test
npm.cmd run build
```

If the build emits generated `src/**/*.js` files, compare them to the initial dirty-path set and remove only files proven to be generated by that command. Never remove a pre-existing user file.

## 8.4 Completion searches

Search executable backend/frontend code for removed mutation identities and routes:

```text
CreatePostReply
DeletePostReply
createReplyWithCount
deleteReplyWithCount
POST /api/posts/:id/replies as a mutation
/api/articles
/api/comments
models.Article
models.Comment
ArticleID
CommentID
article_id
comment_id
```

Search path names separately for generic operational residue:

```text
controllers/article_*.go
controllers/comment_*.go
cmd/requeue-article-embeddings
```

Do not demand that the words Article and Comment disappear from documentation, true PostArticle concepts, user-facing copy, or unrelated historical material.

---

# 9. Definition of Done

Complete only when:

```text
normal detail and active bounded references share publicPostScope
future Post.created_at is not a reference-only visibility rule
canonical reply creation enforces 1000 Unicode runes
canonical create JSON has the 1 MiB request envelope bound
the graph E2E is untagged, truthfully skippable locally, and mandatory in CI
P4 root storage and effective API conversation identity are asserted
P2 Like traverses real Redis relay and PostgreSQL projection seams in integration coverage
P1 relational reaction retention is not misreported as P2 consistency
real Redis cache reload proves parent reply_count after both create and delete
generic source/command paths use Post/PostReply names
all available static, backend, frontend, and integration gates pass
unavailable external gates are reported truthfully
.gitignore and user-owned documentation are untouched
```

---

# 10. Final agent report

Report exactly:

```text
1. initial HEAD and dirty paths
2. files edited and files renamed
3. shared eligibility correction
4. canonical input-limit behavior and tests
5. graph E2E additions and CI execution guard
6. Redis-to-PostgreSQL Like projection test path
7. real create/delete cache-invalidation result
8. remaining Article/Comment occurrences with justification
9. commands run with exact pass/fail/skip counts
10. PostgreSQL/Redis/CI/frontend/runtime checks not executed
11. final git status, with generated artifacts distinguished from user work
```

Do not declare the cutover complete from unit tests alone.
