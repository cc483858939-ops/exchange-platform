# JWT and refresh-session operations

## Runtime model

- Access tokens are Ed25519-signed JWTs using `alg=EdDSA` and a required `kid`.
- Access JWTs contain `iss`, `aud`, `sub`, `sid`, `jti`, `typ`, `iat`, `nbf`, and `exp`.
- Refresh tokens are opaque `rt1.<session-id>.<random-secret>` values, not JWTs.
- Redis stores only a SHA-256 hash of the refresh secret.
- Refresh rotation is one atomic Lua operation. Reuse of a spent refresh token
  revokes the current session family.
- Only API and `all` roles load the JWT private key. Worker and migration roles
  must not receive it.

## Local setup

Generate an untracked key pair:

```powershell
go run ./cmd/gen-jwt-keys --kid local-dev-v1 --out .secrets/jwt
```

Then set the values shown in `.env.example`. The Docker development image uses
the same `.secrets/jwt` paths through the repository bind mount. No key is
generated automatically and the application fails to start if the key is
missing or malformed.

## Production setup

Use either a mounted PKCS#8 private PEM file:

```env
JWT_ACTIVE_KID=jwt-2026-01
JWT_PRIVATE_KEY_FILE=/var/run/secrets/go-exchange/jwt/private.pem
JWT_VERIFY_KEYS_DIR=/var/run/secrets/go-exchange/jwt/public
```

or a Base64-encoded PKCS#8 PEM secret:

```env
JWT_ACTIVE_KID=jwt-2026-01
JWT_PRIVATE_KEY_B64=<base64-pkcs8-private-pem>
```

Do not set both private-key sources. The active public key is derived from the
private key. Extra old public keys may be supplied by directory or through
`JWT_VERIFY_KEYS_B64`, a JSON object whose values are Base64 public PEM files.

## Key rotation

1. Generate the new key pair under a new `kid`.
2. Add the new public key to every API instance.
3. Deploy the new private key and change `JWT_ACTIVE_KID`.
4. Wait for the old access-token TTL plus clock skew.
5. Remove the old public and private keys.

Refresh sessions are independent of the JWT signing key and remain valid during
this rotation.

## Verification

```powershell
go test ./auth ./middlewares ./controllers -count=1
$env:REDIS_TEST_ADDR='127.0.0.1:6379'
go test ./auth -run TestRedisRefreshRotationAllowsExactlyOneConcurrentWinnerIntegration -count=10
```

The Redis integration test requires exactly one winner from 100 simultaneous
refresh attempts and confirms that replay revokes the session.
