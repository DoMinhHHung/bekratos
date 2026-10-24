# Bekratos Identity Service

Microservice quản lý danh tính và xác thực cho hệ thống Bekratos, được xây dựng theo định hướng **security-first** bằng Rust, PostgreSQL/Supabase và Redis/Upstash.

Repo hiện ở giai đoạn bootstrap. Phần triển khai được tổ chức trong `services/identity/` và sẽ hoàn thành theo từng giai đoạn nhỏ, mỗi giai đoạn có nhánh riêng, kiểm thử, rà soát bảo mật và pull request. Cấu trúc cùng các chi tiết bên dưới mô tả **thiết kế mục tiêu**; không có nghĩa mọi module, migration hay workflow đã được triển khai.

## Mục tiêu và phạm vi

Các chức năng dự kiến:

- Đăng ký tài khoản và xác minh email bằng magic link; việc xác nhận được thực hiện qua trang xác nhận và request `POST` rõ ràng.
- Đăng nhập bằng username hoặc email và mật khẩu; đăng xuất.
- Access token dạng JWT ký ES256, công bố khóa công khai qua JWKS.
- Refresh token opaque có rotation, phát hiện reuse và bảo vệ các thao tác đồng thời bằng transaction, khóa hàng và constraint ở PostgreSQL.
- Liệt kê phiên/thiết bị, thu hồi phiên hiện tại hoặc thiết bị khác.
- Quên, đặt lại và đổi mật khẩu; ngăn sử dụng lại 5 mật khẩu gần nhất.
- Xem và cập nhật hồ sơ người dùng.
- Xóa tài khoản với thời gian chờ khôi phục 30 ngày, sau đó purge theo batch.
- Rate limiting, audit security events, gửi email bền vững có retry và đối soát session revocation.
- Health check, readiness check và các kiểm tra phụ thuộc hạ tầng.

**Không thuộc phạm vi phiên bản đầu:** đăng nhập OAuth với nhà cung cấp bên ngoài, MFA và phân quyền RBAC.

## Công nghệ

| Thành phần | Lựa chọn mục tiêu |
|---|---|
| Ngôn ngữ | Rust stable |
| Runtime | Tokio |
| HTTP | Axum |
| Database | PostgreSQL, có thể dùng Supabase |
| Truy vấn SQL | SQLx với query macros và metadata offline trong `.sqlx/` |
| Cache, rate limit, deny-list | Redis, có thể dùng Upstash qua `rediss://` |
| Hash mật khẩu | Argon2id |
| Access token | JWT ký ES256, hỗ trợ nhiều khóa qua JWKS |
| ID | UUIDv7 được sinh trong ứng dụng |
| Thời gian | `TIMESTAMPTZ`, lưu theo UTC |
| Lỗi ứng dụng | `AppError` thống nhất với mã lỗi ổn định, thông điệp và HTTP status |
| Email | Provider được cấu hình qua biến môi trường |

## Nguyên tắc kiến trúc

1. **PostgreSQL là nguồn sự thật.** Redis chỉ phục vụ cache, rate-limit counter và deny-list. Không dùng Redis làm nơi duy nhất quyết định trạng thái tài khoản, credential, refresh token hoặc session.
2. Các flow thay đổi nhiều bảng phải dùng transaction. Constraint, unique index, row lock và thao tác SQL nguyên tử là lớp bảo vệ chính trước race condition.
3. Không tạo bảng outbox riêng trong thiết kế hiện tại. `verification_tokens` đồng thời lưu trạng thái token và hàng đợi gửi email bền vững.
4. Không tạo foreign key xuyên schema. Khi cần liên kết logic giữa các schema, dùng ID và kiểm tra ở tầng nghiệp vụ; các quan hệ trong cùng schema vẫn dùng foreign key khi phù hợp.
5. Handler trong `routes.rs` chỉ parse/validate request ở mức giao thức, gọi service và dựng response. Flow nghiệp vụ nằm trong module use-case; repo thao tác dữ liệu và nhận transaction khi cần.
6. Chia module theo trách nhiệm, không tạo file rỗng chỉ để khớp cây thư mục. Chỉ tách file khi có lý do; ngưỡng khoảng 300–400 dòng là gợi ý, không phải quy tắc cứng.
7. Code sản phẩm không có comment. Tên hàm, kiểu dữ liệu, test và tài liệu bên ngoài code là nơi diễn đạt ý nghĩa.
8. Không ghi bí mật hoặc dữ liệu nhạy cảm vào log, test fixture, lỗi trả về, artifact CI hay Git.
9. Không dùng check-then-insert để bảo vệ tính duy nhất; dựa vào unique constraint và xử lý kết quả vi phạm một cách an toàn.
10. Với endpoint có nguy cơ account enumeration, phản hồi phải trung tính và rate limit phải được áp dụng.

## Cấu trúc repository mục tiêu

```text
bekratos/
├── .github/
│   ├── workflows/
│   │   ├── ci.yml
│   │   └── security.yml
│   └── dependabot.yml
├── docs/
│   ├── grok-instructions.md
│   └── grok-start-prompt.md
├── services/
│   └── identity/
│       ├── Cargo.toml
│       ├── Dockerfile
│       ├── migrations/
│       ├── .sqlx/
│       ├── src/
│       │   ├── main.rs
│       │   ├── lib.rs
│       │   ├── app.rs
│       │   ├── config.rs
│       │   ├── error.rs
│       │   ├── state.rs
│       │   ├── infra/
│       │   │   ├── db.rs
│       │   │   ├── redis.rs
│       │   │   ├── mailer.rs
│       │   │   ├── password.rs
│       │   │   ├── tokens.rs
│       │   │   ├── jwt.rs
│       │   │   └── crypto.rs
│       │   ├── middleware/
│       │   │   ├── auth.rs
│       │   │   ├── rate_limit.rs
│       │   │   ├── request_id.rs
│       │   │   └── security_headers.rs
│       │   ├── modules/
│       │   │   ├── auth/
│       │   │   ├── user/
│       │   │   ├── session/
│       │   │   ├── credential/
│       │   │   ├── token/
│       │   │   ├── audit/
│       │   │   └── jwks/
│       │   └── jobs/
│       │       ├── email_worker.rs
│       │       ├── purge_accounts.rs
│       │       ├── rotate_keys.rs
│       │       └── revocation_reconcile.rs
│       └── tests/
│           ├── common/mod.rs
│           └── integration tests theo từng flow
├── Cargo.toml
├── Cargo.lock
├── deny.toml
├── clippy.toml
├── rustfmt.toml
├── rust-toolchain.toml
├── docker-compose.yml
├── .env.example
└── README.md
```

Các nhóm module dự kiến:

- `auth`: routes, DTO và use-case đăng ký, xác minh email, đăng nhập, đăng xuất, refresh, password và restore.
- `user`: routes, DTO, service và repository cho profile/tài khoản.
- `session`: quản lý session và thu hồi thiết bị.
- `credential`: repository cho password credential và password history.
- `token`: repository cho token xác minh/reset/restore.
- `audit`: repository và kiểu dữ liệu cho security events.
- `jwks`: routes, service, repository và vòng đời khóa ký.

## Mô hình dữ liệu mục tiêu

Tên schema dưới đây mang tính khái niệm, dự kiến phân nhóm thành `user`, `credential`, `token`, `session`, `jwks` và `audit`. Tên schema/bảng cuối cùng phải thống nhất trong migration trước khi code phụ thuộc vào chúng.

### User

| Bảng | Trường chính | Ràng buộc và ghi chú |
|---|---|---|
| `users` | `id`, `status`, `status_before_deletion`, `locale`, `timezone`, `deletion_requested_at`, `purge_after`, `created_at`, `updated_at` | `status`: `pending`, `active`, `suspended`, `deleted`; `purge_after` chỉ được thiết lập khi `status = deleted`; trạng thái trước khi xóa được giữ để khôi phục chính xác. |
| `user_profile` | `user_id`, `full_name`, `display_name`, `avatar_url`, `created_at`, `updated_at` | `user_id` là PK và FK đến `users.id`; giới hạn độ dài và validate URL theo chính sách ứng dụng. |
| `user_emails` | `id`, `user_id`, `email`, `is_primary`, `verified_at`, `created_at`, `updated_at` | `email` dùng kiểu không phân biệt hoa/thường phù hợp như `citext`; unique email toàn cục; unique partial index để mỗi user chỉ có một email primary. |
| `user_usernames` | `id`, `user_id`, `username`, `created_at`, `updated_at` | `username` không phân biệt hoa/thường; `UNIQUE(username)`, `UNIQUE(user_id)`; regex dự kiến `^[a-z0-9_.]{3,30}$`. |
| `policy_acceptances` | `id`, `user_id`, `policy_type`, `version`, `document_sha256`, `accepted_at`, `ip`, `user_agent` | `policy_type` là `terms` hoặc `privacy`; có index theo `user_id`; áp dụng chính sách retention cho IP và user-agent. |

### Credential

| Bảng | Trường chính | Ràng buộc và ghi chú |
|---|---|---|
| `password_credentials` | `user_id`, `password_hash`, `password_changed_at`, `created_at`, `updated_at` | `user_id` là PK; lưu PHC string của Argon2id. |
| `password_history` | `id`, `user_id`, `password_hash`, `created_at` | Giữ 5 hash gần nhất, bao gồm mật khẩu hiện tại; các hash cũ được xóa khi vượt giới hạn chính sách. |

### Token và hàng đợi email

| Bảng | Trường chính | Ràng buộc và ghi chú |
|---|---|---|
| `verification_tokens` | `id`, `user_id`, `purpose`, `email`, `expires_at`, `consumed_at`, `revoked_at`, `sent_at`, `delivery_attempts`, `next_attempt_at`, `lease_until`, `last_delivery_error_code`, `created_at` | `purpose`: `email_verify`, `password_reset`, `account_restore`; `expires_at > created_at`; index cho token còn hiệu lực theo user/purpose và index cho việc tìm email đến hạn gửi. Dùng làm hàng đợi email bền vững. |

Magic link dự kiến có dạng `token_id.mac`, trong đó `mac = HMAC-SHA256(server_secret, token_id || purpose)`. Cần mã hóa đầu vào theo biểu diễn canonical, có domain separation, so sánh MAC constant-time và quản lý phiên bản khóa để hỗ trợ xoay secret. Database không lưu dữ liệu đủ để dựng MAC nếu không có `server_secret`. Nếu cả database lẫn secret phía server bị lộ, token đang còn hiệu lực có thể bị giả mạo; vì vậy secret phải được bảo vệ, giới hạn quyền truy cập và có quy trình rotation.

Token một lần phải được consume nguyên tử ở PostgreSQL. Câu SQL minh họa:

```sql
UPDATE token.verification_tokens
SET consumed_at = now()
WHERE id = $1
  AND purpose = $2
  AND consumed_at IS NULL
  AND revoked_at IS NULL
  AND expires_at > now()
RETURNING user_id, email;
```

Tên schema trong câu SQL là minh họa; phải khớp với migration cuối cùng. Flow thực tế cần kết hợp cập nhật token với các thay đổi trạng thái nghiệp vụ trong cùng transaction.

### Session và refresh token

| Bảng | Trường chính | Ràng buộc và ghi chú |
|---|---|---|
| `sessions` | `id` (`sid`), `user_id`, `ip`, `user_agent`, `device_name`, `last_seen_at`, `expires_at`, `revoked_at`, `revoked_reason`, `created_at` | Index theo user cho session còn hiệu lực. `sid` được đưa vào claim JWT để liên kết access token với session. |
| `refresh_tokens` | `id`, `session_id`, `parent_id`, `token_hash`, `expires_at`, `used_at`, `revoked_at`, `created_at` | Unique `token_hash`; FK `session_id`; unique partial index cho một refresh token chưa dùng/chưa revoke trên mỗi session; unique partial index cho `parent_id` để mỗi token chỉ có một token con. Thời hạn token con không vượt quá `sessions.expires_at`. |

### JWKS và audit

| Bảng | Trường chính | Ràng buộc và ghi chú |
|---|---|---|
| `signing_keys` | `id`, `kid`, `alg`, `public_jwk`, `private_key_enc`, `key_version`, `status`, `publish_at`, `activated_at`, `retire_at`, `publish_until`, `created_at` | `alg` là `ES256`; status: `pending`, `active`, `retiring`, `retired`; unique `kid`; unique partial index bảo đảm chỉ một khóa active. Private key được mã hóa bằng AEAD với khóa mã hóa nằm ngoài database. |
| `security_events` | `id`, `event_type`, `user_id`, `session_id`, `ip`, `user_agent`, `metadata`, `created_at` | Bảng append-only ở tầng ứng dụng; không lưu password, token, magic link, authorization header hay private key; quy định retention cho IP/user-agent. |

Các sự kiện tối thiểu cần ghi: đăng nhập thành công/thất bại, reset/đổi mật khẩu, đăng xuất, thu hồi session, phát hiện refresh-token reuse, xóa/khôi phục tài khoản và xoay khóa.

## Redis keyspace mục tiêu

| Key | Mục đích |
|---|---|
| `jwks:cache` | Cache JWKS có TTL. |
| `user:{id}` | Cache dữ liệu hiển thị của user; không dùng để quyết định bảo mật. |
| `login_fail:ip:{hash}` | Đếm đăng nhập thất bại theo IP đã hash. |
| `login_fail:acct:{hash}` | Đếm đăng nhập thất bại theo định danh tài khoản đã hash. |
| `lockout:{hash}` | Theo dõi khóa tạm theo chính sách chống brute-force. |
| `rate_limit:{endpoint}:ip:{hash}` | Giới hạn request theo endpoint và IP. |
| `rate_limit:{endpoint}:id:{hash}` | Giới hạn request theo endpoint và định danh tài khoản. |
| `sid_revoked:{sid}` | Deny-list session đã bị thu hồi; TTL không vượt thời hạn access token còn lại. |
| `resend_cooldown:{purpose}:{user_id}` | Cooldown yêu cầu gửi lại magic link. |
| `idem:{key}` | Hỗ trợ idempotency ở các endpoint phù hợp. |

Các định danh như email, username và IP phải được chuẩn hóa và hash trước khi đưa vào Redis key. Các thao tác tăng/kiểm tra rate limit cần nguyên tử. Không dùng cache user hoặc cache active-session làm nguồn sự thật.

## API mục tiêu

Các đường dẫn dưới đây là thiết kế dự kiến; request/response schema và mã lỗi sẽ được chốt trong từng giai đoạn triển khai.

| Method | Đường dẫn | Mô tả |
|---|---|---|
| `POST` | `/v1/auth/register` | Đăng ký tài khoản. |
| `POST` | `/v1/auth/verify-email` | Xác nhận magic link sau khi người dùng bấm nút xác nhận trên trang. |
| `POST` | `/v1/auth/resend-verification` | Gửi lại email xác minh. |
| `POST` | `/v1/auth/login` | Đăng nhập bằng username hoặc email và mật khẩu. |
| `POST` | `/v1/auth/refresh` | Xoay refresh token. |
| `POST` | `/v1/auth/logout` | Đăng xuất session hiện tại. |
| `POST` | `/v1/auth/password/forgot` | Yêu cầu đặt lại mật khẩu. |
| `POST` | `/v1/auth/password/reset` | Đặt lại mật khẩu bằng token. |
| `POST` | `/v1/auth/password/change` | Đổi mật khẩu khi đã xác thực mật khẩu cũ. |
| `POST` | `/v1/auth/account/restore/request` | Yêu cầu khôi phục tài khoản. |
| `POST` | `/v1/auth/account/restore/confirm` | Xác nhận khôi phục tài khoản. |
| `GET`, `PATCH` | `/v1/me` | Xem và sửa profile. |
| `DELETE` | `/v1/me` | Xóa tài khoản, yêu cầu xác minh lại bằng mật khẩu. |
| `GET` | `/v1/me/sessions` | Liệt kê thiết bị/session. |
| `DELETE` | `/v1/me/sessions/{id}` | Thu hồi một thiết bị/session. |
| `GET` | `/.well-known/jwks.json` | Công bố khóa công khai. |
| `GET` | `/healthz` | Liveness/health check. |
| `GET` | `/readyz` | Readiness check phụ thuộc cấu hình vận hành. |

## Luồng nghiệp vụ và bất biến bảo mật

### Đăng ký và xác minh email

1. Rate limit theo IP và email; validate email, username, độ mạnh mật khẩu và yêu cầu `accept_policy = true`.
2. Trong một transaction, tạo `users` ở trạng thái `pending`, profile, email, username, password credential, password history, policy acceptance và token `email_verify`.
3. Dựa vào unique constraint để xử lý trùng email/username; không dựa vào check-then-insert. Phản hồi ở endpoint nhạy cảm với enumeration phải trung tính.
4. Nếu email đã thuộc tài khoản `pending`, không xóa hoặc ghi đè tài khoản cũ. Vô hiệu token cũ, phát hành token mới và trả phản hồi tương đương.
5. Worker gửi magic link. `GET` mở link không được tự tiêu thụ token; người dùng xem trang xác nhận và thực hiện `POST /v1/auth/verify-email`.
6. Xác minh MAC và consume token nguyên tử; cập nhật `verified_at` và chuyển tài khoản sang `active` trong transaction.

### Đăng nhập

1. Áp dụng rate limit và kiểm tra bộ đếm thất bại/lockout trong Redis.
2. Tìm user theo username hoặc email. Nếu không tìm thấy, vẫn chạy dummy Argon2id verification để giảm khác biệt thời gian.
3. Verify mật khẩu bằng Argon2id trong `spawn_blocking`; khi sai, tăng bộ đếm và trả lỗi chung.
4. Chỉ tài khoản `active` được tạo session. Với `pending`, yêu cầu xác minh email; với `deleted`, chỉ gợi ý khôi phục sau khi mật khẩu đúng. Không để trạng thái tài khoản bị lộ qua phản hồi đăng nhập trước khi chứng minh quyền kiểm soát credential.
5. Tạo session và refresh token, sau đó ký access token ES256. Không trả thông tin bí mật trong log hoặc lỗi.
6. Xóa bộ đếm phù hợp khi đăng nhập thành công; rehash mật khẩu nếu tham số Argon2id hiện tại đã thay đổi.

### Refresh token và reuse detection

1. Refresh token là chuỗi opaque sinh từ CSPRNG; database chỉ lưu hash. Trong transaction, khóa hàng token và session bằng `SELECT ... FOR UPDATE`.
2. Nếu token đã được dùng hoặc revoke, xem là reuse: revoke toàn bộ session và refresh token liên quan, ghi `security_events`, rồi trả `401`. Không có grace period.
3. Nếu hợp lệ, đánh dấu token cũ đã dùng, tạo chính xác một token con có `parent_id` trỏ về token cũ và `expires_at` không vượt quá thời hạn session, tạo access token mới rồi commit trước khi trả response.
4. Client phải dùng single-flight, chỉ có một refresh đang chạy tại một thời điểm. Nếu client mất response sau khi token đã rotate, yêu cầu đăng nhập lại thay vì thử lại token cũ.
5. Unique constraint và transaction phải ngăn việc phát hành nhiều token con khi có request đồng thời.

### Đăng xuất và thu hồi session

1. Trong transaction PostgreSQL, revoke session và toàn bộ refresh token thuộc session đó.
2. Sau commit, ghi `sid_revoked:{sid}` vào Redis. Nếu ghi thất bại, retry và để job đối soát bù lại.
3. Khi thu hồi thiết bị khác, xác nhận session thuộc user hiện tại rồi xử lý tương tự với reason như `revoked_by_user`.
4. Endpoint nhạy cảm như đổi mật khẩu, xóa tài khoản, thu hồi thiết bị và liệt kê session phải đọc trạng thái session từ PostgreSQL.

### Quên, đặt lại và đổi mật khẩu

- **Quên mật khẩu:** rate limit theo IP và định danh; luôn trả phản hồi trung tính. Nếu tài khoản tồn tại, revoke token `password_reset` cũ, tạo token mới và gửi tới email chính đã xác minh.
- **Đặt lại mật khẩu:** kiểm tra token và hạn dùng, không cho dùng lại một trong 5 mật khẩu gần nhất; cập nhật credential, thêm password history, consume token và revoke mọi session/refresh token. Các thay đổi phải thuộc cùng transaction.
- **Đổi mật khẩu:** yêu cầu mật khẩu hiện tại; kiểm tra lịch sử mật khẩu; cập nhật credential và history; revoke các session khác nhưng giữ session hiện tại theo chính sách. Đọc session trực tiếp từ PostgreSQL.
- Gửi email cảnh báo sau khi mật khẩu được đổi hoặc reset thành công. Không đưa token hay URL hoàn chỉnh vào log.

### Xóa và khôi phục tài khoản

- **Xóa:** yêu cầu xác minh lại bằng mật khẩu và kiểm tra session từ PostgreSQL. Lưu `status_before_deletion`, đặt `status = deleted`, `deletion_requested_at = now()` và `purge_after = now() + interval '30 days'`, revoke mọi session và vô hiệu token đang chờ. Giữ email/username trong thời gian khôi phục.
- **Purge:** worker xử lý theo batch, idempotent, dùng `FOR UPDATE SKIP LOCKED`; chỉ xử lý tài khoản vẫn ở trạng thái `deleted` và đã quá `purge_after`. Xóa hoặc ẩn danh dữ liệu phụ thuộc theo chính sách retention.
- **Yêu cầu khôi phục:** rate limit và luôn trả phản hồi trung tính. Chỉ phát token cho tài khoản đang `deleted`, chưa quá hạn purge và có email đã xác minh.
- **Xác nhận khôi phục:** consume token và dùng điều kiện trạng thái/hạn trong transaction; khôi phục `status = status_before_deletion` để tài khoản từng `suspended` không bị tự chuyển thành `active`; xóa `deletion_requested_at` và `purge_after`. Không tự đăng nhập, người dùng cần đăng nhập lại để tạo session mới.

### Worker gửi email

1. Token và công việc gửi email được tạo trong cùng transaction nghiệp vụ.
2. Worker claim batch bằng `FOR UPDATE SKIP LOCKED`, đặt `lease_until`, rồi commit transaction ngắn trước khi gọi provider.
3. Chỉ gửi token chưa consume, chưa revoke và chưa hết hạn. Khi thành công, cập nhật `sent_at`.
4. Với lỗi tạm thời, tăng `delivery_attempts` và đặt `next_attempt_at` theo exponential backoff có giới hạn. Quá số lần thử thì dừng và phát cảnh báo; người dùng có thể yêu cầu resend.
5. Retry dùng lại token hiện có, không tạo token mới. Vì provider có thể nhận email nhưng worker chưa kịp ghi nhận thành công, hệ thống chấp nhận giao hàng at-least-once và email trùng.
6. Worker cần xử lý lease hết hạn sau crash, trạng thái token thay đổi trong lúc chờ gửi và lỗi provider một cách idempotent.

### Session revocation và giới hạn độ trễ

- Redis chỉ giữ deny-list `sid_revoked`; không cache trạng thái active để ra quyết định bảo mật.
- Job đối soát chạy khoảng mỗi phút, đọc session đã bị revoke trong khoảng TTL của access token và ghi lại marker vào Redis.
- Cửa sổ nhận biết revoke ở các request chỉ dựa vào deny-list bị giới hạn bởi chu kỳ đối soát và thời hạn access token. Đây là giới hạn bảo mật cần được công bố, đo đạc và kiểm thử; không giả định revoke phân tán là tức thời.
- Endpoint nhạy cảm luôn đọc PostgreSQL. Chính sách xử lý khi PostgreSQL hoặc Redis không khả dụng phải được chọn rõ ràng; không tự ý fail-open ở nơi có thể cho phép thao tác nhạy cảm.

### JWT, JWKS và xoay khóa

Access token dự kiến chứa các claim `iss`, `sub`, `aud`, `iat`, `nbf`, `exp`, `jti` và `sid`; thời hạn access token dự kiến 10–15 phút. Header dùng `alg = ES256` và `kid`. Verifier phải allowlist thuật toán, kiểm tra issuer, audience, expiry, not-before, key ID và session/revocation theo chính sách; không tin thuật toán do token tự chọn.

Quy trình xoay khóa thông thường:

1. Tạo khóa mới ở trạng thái `pending`; khóa này chưa dùng để ký. Private key được mã hóa AEAD bằng khóa mã hóa lưu ngoài database.
2. Publish public key mới vào JWKS và chờ ít nhất TTL cache JWKS để verifier có cơ hội nhận khóa trước khi token được ký bằng nó.
3. Chuyển khóa mới thành `active`; khóa cũ chuyển sang `retiring` và chỉ dùng xác minh token cũ.
4. `publish_until` của khóa cũ phải đủ dài để bao phủ `retire_at + access-token TTL + clock skew + JWKS cache TTL`.
5. Sau `publish_until`, chuyển khóa sang `retired` và loại khỏi JWKS.
6. Khi nghi ngờ private key bị lộ, dùng quy trình khẩn cấp riêng: ngừng publish khóa đó ngay và yêu cầu xác thực lại theo chính sách incident response.

JWKS cache có TTL. Khi gặp `kid` chưa biết, verifier có thể refresh JWKS nhưng phải giới hạn tần suất để tránh bị lợi dụng gây request storm.

## Bảo mật vận hành

- Dùng Argon2id với tham số được benchmark trong môi trường triển khai; chạy hash/verify trong `spawn_blocking` để không chặn async executor.
- Token nhạy cảm sinh từ CSPRNG hoặc theo thiết kế HMAC đã review; MAC so sánh constant-time và token một lần consume nguyên tử tại SQL.
- Refresh token luôn opaque, chỉ lưu hash; tuyệt đối không log token gốc.
- Đăng ký, quên mật khẩu, resend và yêu cầu khôi phục cần response chống enumeration và rate limit. Đăng nhập trả lỗi chung và dùng dummy hash khi tài khoản không tồn tại.
- Rate limit phải dùng thao tác Redis nguyên tử. Thiết kế lockout không được cho phép kẻ khác dễ dàng khóa tài khoản nạn nhân chỉ bằng cách cố tình nhập sai mật khẩu.
- Chỉ tin `X-Forwarded-For` hoặc header proxy tương tự khi request đi qua proxy nằm trong `TRUSTED_PROXIES`; mặc định header từ client là không đáng tin.
- Không log password, token, magic link, authorization header, private key, dữ liệu xác thực hoặc PII không cần thiết. Không đưa secret vào fixture, snapshot, CI artifact hay thông báo lỗi.
- Giới hạn body size, thiết lập timeout, CORS allowlist chặt, security headers và request ID; không để request ID chứa dữ liệu do người dùng kiểm soát mà không chuẩn hóa.
- Secret đọc từ môi trường hoặc secret manager, không commit vào Git. Không dùng credential production cho local development hay CI.
- Gửi cảnh báo qua email hoặc kênh vận hành phù hợp khi đổi/reset mật khẩu, tạo session mới, xóa/khôi phục tài khoản và các sự kiện bảo mật quan trọng.
- Migration cần được review như code bảo mật: kiểm tra constraint, index, khóa hàng, thứ tự rollout, khả năng rollback và tác động lên dữ liệu hiện có.
- Thực hiện dependency/advisory scanning, secret scanning, cập nhật dependency có kiểm soát và giới hạn quyền trong CI ở mức tối thiểu.

## Cấu hình môi trường

Danh sách dưới đây là các biến cấu hình dự kiến; `.env.example` cần được cập nhật đồng bộ với `config.rs` khi giai đoạn bootstrap hoàn tất.

| Biến | Ý nghĩa |
|---|---|
| `BIND_ADDR` | Địa chỉ và cổng HTTP service lắng nghe. |
| `DATABASE_URL` | Chuỗi kết nối PostgreSQL. |
| `REDIS_URL` | Redis URL; dùng `rediss://` khi provider yêu cầu TLS, ví dụ Upstash. |
| `JWT_ISSUER`, `JWT_AUDIENCE` | Giá trị `iss` và `aud` của access token. |
| `ACCESS_TOKEN_TTL_SECS` | Thời hạn access token. |
| `REFRESH_TOKEN_TTL_SECS` | Thời hạn refresh token, không vượt thời hạn session. |
| `SESSION_ABSOLUTE_TTL_SECS` | Thời hạn tuyệt đối của session. |
| `TOKEN_HMAC_SECRET` | Secret phía server dùng tạo MAC cho magic link; không phải giá trị public và không được commit. |
| `KEY_ENCRYPTION_KEY` | Khóa AEAD 32 byte được encode base64 để mã hóa private key; phải quản lý như secret cấp cao. |
| `APP_BASE_URL` | Origin đáng tin cậy dùng dựng link xác minh/reset/restore. |
| `TRUSTED_PROXIES` | Danh sách proxy được tin cậy để diễn giải client IP. |
| `MAILER_*` | Cấu hình provider email, sender và credential liên quan. |

Không ghi giá trị secret thật trong tài liệu hoặc ví dụ. `.env.example` chỉ chứa placeholder không dùng được để truy cập dịch vụ.

## Chạy local

Các lệnh dưới đây là luồng phát triển dự kiến sau khi workspace, compose file, migration và `.env.example` đã được tạo. Không chạy với credential production.

```bash
docker compose up -d
cp .env.example .env
sqlx migrate run --source services/identity/migrations
cargo run -p identity
```

Nếu dự án được cấu hình chạy từ thư mục service thay vì workspace root, điều chỉnh working directory và package command cho khớp với manifest thực tế.

### Kiểm tra code

Từ workspace root, các lệnh mục tiêu:

```bash
cargo fmt --all -- --check
cargo clippy --workspace --all-targets --all-features --locked -- -D warnings
cargo test --workspace --all-features --locked
SQLX_OFFLINE=true cargo check --workspace --locked
cargo sqlx prepare --workspace --check -- --all-targets --all-features
cargo deny check
```

Các truy vấn dùng SQLx macros phải có metadata `.sqlx/` được cập nhật và kiểm tra ở chế độ offline. Migration phải được thử trên database sạch và trên đường nâng cấp từ phiên bản trước. CI cần dùng PostgreSQL/Redis dùng riêng cho test, không sử dụng dữ liệu thật.

### Kiểm thử bảo mật bắt buộc

- Hai request refresh đồng thời; token refresh được dùng hai lần; đảm bảo một token chỉ có một token con.
- Replay token reset/verify/restore và race condition khi consume token.
- Race insert email hoặc username trùng.
- Khôi phục tài khoản sát thời điểm `purge_after`, sau hạn purge và khi trạng thái trước xóa là `suspended`.
- Xoay khóa JWKS, cache TTL, token được ký bằng khóa cũ và `kid` không tồn tại.
- Redis lỗi sau khi revoke session; worker reconciliation phục hồi deny-list.
- Worker email crash sau khi claim, lease hết hạn, provider timeout và email gửi trùng.
- Lockout/rate limit chống brute-force, phản hồi chống enumeration và proxy header không đáng tin.
- Migration trên database sạch và upgrade; purge idempotent khi chạy đồng thời.

## CI và tự động hóa

Mục tiêu CI sau khi workspace được triển khai:

- `ci.yml`: kiểm tra tài liệu/cấu trúc repository, định dạng Rust, Clippy, test với PostgreSQL 17 và Redis 7, SQLx offline metadata, `cargo-deny` và Docker build. Có job tổng hợp `ci-success` để dùng làm required check trong branch protection.
- `security.yml`: secret scanning bằng Gitleaks và quét advisory theo lịch.
- `dependabot.yml`: cập nhật Cargo và GitHub Actions theo lịch.
- Khi repo chưa có `services/identity/Cargo.toml`, workflow có thể bỏ qua các bước Rust với thông báo rõ ràng thay vì báo lỗi do chưa có manifest.

Đây là **mục tiêu cấu hình**, không phải xác nhận rằng tất cả workflow/job đã tồn tại hoặc đang xanh. Chỉ đánh dấu CI đạt khi run thực tế đã hoàn tất thành công và các required check đã được cấu hình trên GitHub.

## Lộ trình triển khai

| Giai đoạn | Phạm vi |
|---|---|
| 0 | Workspace baseline, config, error, state, health check, Dockerfile, docker-compose, `.env.example` và CI nền tảng. |
| 1 | Migrations, toàn bộ constraint/index và kiểm thử schema trên database sạch/upgrade. |
| 2 | Hạ tầng database, Redis, password, token, crypto, JWT và mailer. |
| 3 | JWKS, quản lý khóa ký và xoay khóa. |
| 4 | Đăng ký, xác minh email, đăng nhập, refresh, đăng xuất và session/device. |
| 5 | Quên/đổi/reset mật khẩu, profile, xóa và khôi phục tài khoản. |
| 6 | Email worker, purge worker, revocation reconciliation, rate limit và audit. |
| 7 | Hardening, kiểm thử tải, rà soát bảo mật, tài liệu vận hành và hoàn thiện phát hành. |

Mỗi giai đoạn phải được làm trên một branch riêng, hoàn tất test và review các rủi ro liên quan trước khi tạo pull request. Không gộp nhiều giai đoạn bảo mật lớn vào một thay đổi khó review. Hướng dẫn thực thi và prompt khởi đầu được quản lý trong `docs/`.

## Trạng thái dự án

**Trạng thái: bootstrap ban đầu; chưa sẵn sàng cho production.** Chỉ được xem là production-ready sau khi implementation, migrations, integration tests, secret management, cấu hình vận hành, kiểm soát triển khai, giám sát/alert và quy trình xử lý sự cố đã hoàn tất và được xác minh.
