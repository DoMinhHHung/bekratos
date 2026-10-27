# Bekratos Identity Service

Identity Service là microservice Rust quản lý tài khoản nền tảng Bekratos: đăng ký, xác minh email, đăng nhập, session, credential, profile và vòng đời tài khoản. Service dùng PostgreSQL làm nguồn sự thật cho identity/security state, Redis cho rate limiting, và ES256 JWT/JWKS cho access token.

> **Phạm vi repo hiện tại:** source tree được kiểm tra trên branch `dev` ngày 2026-10-26 có implementation của `services/identity/`, migration, Docker, CI và integration/security tests. Đây không còn chỉ là repo bootstrap/Phase 0. Tuy nhiên, việc các route tồn tại và tests có trong repo không chứng minh rằng tất cả đã chạy thành công hoặc service đã production-ready. Kiểm tra trạng thái CI thực tế trước khi release.

Control Plane hiện có source Go và quản lý organizations/projects/modules/configuration/API-key lifecycle cùng deployment intent. Identity vẫn là nguồn sự thật cho tài khoản/session của người đăng nhập Dashboard. Backend Runtime/server thực thi workload chưa có implementation trong repository; HTTP contract của Control Plane không tự tạo hạ tầng.

## Phạm vi Identity

Các route hiện có trong source:

| Method | Route | Mục đích |
|---|---|---|
| `GET` | `/healthz` | Liveness |
| `GET` | `/readyz` | Readiness theo cấu hình database/Redis |
| `POST` | `/v1/auth/register` | Đăng ký tài khoản, tạo token và đưa email vào queue |
| `POST` | `/v1/auth/verify-email` | Xác minh email bằng token dùng một lần |
| `POST` | `/v1/auth/resend-verification` | Yêu cầu gửi lại email xác minh |
| `POST` | `/v1/auth/login` | Đăng nhập bằng email/username và mật khẩu |
| `POST` | `/v1/auth/logout` | Thu hồi session hiện tại |
| `POST` | `/v1/auth/refresh` | Refresh token rotation |
| `POST` | `/v1/auth/forgot-password` | Yêu cầu reset mật khẩu; phản hồi trung tính |
| `POST` | `/v1/auth/reset-password` | Đặt lại mật khẩu bằng token |
| `POST` | `/v1/auth/change-password` | Đổi mật khẩu |
| `POST` | `/v1/auth/request-restore` | Yêu cầu khôi phục tài khoản đã xóa |
| `POST` | `/v1/auth/restore` | Xác nhận khôi phục tài khoản |
| `GET` | `/v1/me` | Xem profile hiện tại |
| `PATCH` | `/v1/me` | Cập nhật profile hiện tại |
| `GET` | `/v1/me/sessions` | Liệt kê session/thiết bị |
| `DELETE` | `/v1/me/sessions` | Thu hồi các session khác theo logic hiện tại |
| `DELETE` | `/v1/me/sessions/{id}` | Thu hồi một session |
| `DELETE` | `/v1/me/delete` | Yêu cầu xóa tài khoản |
| `GET` | `/v1/jwks` | Công bố JSON Web Key Set |
| `GET` | `/swagger-ui/` | Swagger UI |
| `GET` | `/api-docs/openapi.json` | OpenAPI document |

Đây là danh sách route được đăng ký trong source, không phải lời khẳng định rằng từng flow đã vượt qua kiểm thử end-to-end trên môi trường triển khai. Endpoint JWKS hiện là `/v1/jwks`; không giả định rằng `/.well-known/jwks.json` đã được đăng ký.

## Công nghệ và cấu trúc

- Rust stable, Tokio, Axum.
- PostgreSQL với SQLx; migration được đặt trong `services/identity/migrations/`.
- Redis dùng cho rate limiting và readiness check.
- Argon2id cho password hashing.
- Access token JWT ký ES256; JWKS công bố public key.
- Refresh token opaque được hash và rotation được quản lý bằng PostgreSQL.
- HMAC-SHA-256 cho magic-link token; keyring cấu hình qua environment.
- Worker gồm email-delivery queue, purge tài khoản đã quá thời hạn khôi phục và vòng đời retirement của signing keys.
- Docker multi-stage, runtime non-root, health check.

Mã nguồn chính nằm trong `services/identity/src/`, migration trong `services/identity/migrations/`, integration/security tests trong `services/identity/tests/`. Bảng migration hiện có các nhóm `user`, `credential`, `token`, `session`, `jwks`, `audit`, gồm tài khoản/profile/email/username, password credentials/history, verification tokens, sessions/refresh tokens, signing keys và security events.

## Các bất biến bảo mật thiết kế

- PostgreSQL là nguồn sự thật cho tài khoản, credential, token và session. Thay đổi nhiều bảng phải nằm trong transaction.
- Unique constraints, partial unique indexes, khóa hàng và cập nhật nguyên tử bảo vệ tính duy nhất, token consumption và refresh rotation.
- Redis không thay thế nguồn sự thật PostgreSQL. Lỗi Redis có thể làm các flow phụ thuộc rate limiter trả lỗi; đừng tự giả định fail-open.
- Magic-link và refresh-token bí mật không được ghi log; token một lần phải hết hạn, có thể revoke và chỉ consume một lần.
- Access token phải được kiểm tra chữ ký, thuật toán ES256, issuer, audience, expiry, not-before và `kid`.
- Không đưa quyền tổ chức/project vào JWT Identity rồi coi đó là dữ liệu luôn mới. Control Plane phải đánh giá membership/role theo dữ liệu hiện tại.
- Không tin các identity/tenant ID do client tự gửi để xác định quyền truy cập.
- Không log password, access/refresh token, magic link, cookie, Authorization header, private key hoặc PII không cần thiết.

## Lưu ý về email trong source hiện tại

Identity có adapter gửi email qua Resend (`MAIL_PROVIDER=resend`) bằng HTTPS, timeout hữu hạn, response limit và không log recipient, nội dung magic link hay API key. `MAIL_PROVIDER=console` chỉ mô phỏng gửi và `none` trả lỗi; cả hai bị từ chối khi `APP_ENV=production`. Cấu hình production cần `APP_ENV=production`, `MAIL_PROVIDER=resend`, `MAIL_API_KEY`, sender đã xác minh ở Resend, `MAIL_API_BASE_URL=https://api.resend.com` và `APP_PUBLIC_BASE_URL` HTTPS. Worker vẫn dùng retry/backoff; cần test gửi tới domain đã xác minh trong staging trước release. Không đưa API key thật vào repo.

## Tích hợp Control Plane — hợp đồng cần chốt

Control Plane dùng Identity để xác thực người đăng nhập Dashboard; project end-users là danh tính khác, thuộc Auth module của runtime theo từng project/environment.

Identity cung cấp private endpoint `POST /internal/v1/identity/introspect`. Request body là `{"access_token":"<platform-access-jwt>"}`; caller phải gửi `X-Identity-Service-Token`. Response trả `active`, `user_id`, `session_id`, `issuer`, `audience`, `expires_at`, `email_verified` và `verified_primary_email`. Endpoint kiểm tra chữ ký/claims ES256, rồi đọc session/account/primary verified email từ PostgreSQL. Response có `Cache-Control: no-store`; lỗi DB/config trả lỗi dịch vụ thay vì suy đoán session còn hợp lệ.

Để bật contract, đặt cùng secret random tối thiểu 32 ký tự vào `IDENTITY_SERVICE_API_TOKEN` ở Identity và Control Plane. Control Plane kiểm tra JWT/JWKS và gọi introspection cho mỗi request quản trị; 401 làm request bị từ chối, lỗi Identity/network làm request fail-closed với 503. Không tin `X-User-ID`, email hoặc role do caller tự truyền. Endpoint hiện dùng shared-secret header; production cần giới hạn network, ưu tiên mTLS/service identity và quản lý secret bằng secret manager. Không cache kết quả introspection nếu cần thu hồi session tức thời.

## Chạy local

Dùng PostgreSQL và Redis **disposable/local**. Từ thư mục service:

```bash
cd services/identity
cp .env.example .env
```

Trước khi chạy, thay các key mẫu bằng secret ngẫu nhiên chỉ dùng local. Không tái sử dụng key trong `.env.example`, không dùng dữ liệu local ở production. Có thể khởi động các dependency và service bằng:

```bash
docker compose up --build
```

Compose file nằm tại `services/identity/docker-compose.yml`. Service mặc định lắng nghe port `8080` trong container; đọc `.env.example`, `src/config.rs` và `docs/operations.md` để xác nhận cấu hình mà phiên bản source hiện tại yêu cầu.

**Email:** local `MAIL_PROVIDER=console` không gửi email. Để thử gửi thật, dùng API key Resend của môi trường dev và sender/domain đã xác minh; không dùng API key production cho local/test.

## Kiểm tra chất lượng

Chạy từ `services/identity/`:

```bash
cargo fmt --all -- --check
cargo clippy --locked --all-targets --all-features -- -D warnings
SQLX_OFFLINE=true cargo check --locked --all-targets
SQLX_OFFLINE=true cargo test --locked --all-targets
cargo audit --deny warnings
```

Integration tests cần `DATABASE_URL_TEST` trỏ tới database disposable và `REDIS_URL_TEST` trỏ tới Redis disposable. Không bao giờ dùng database/Redis production cho test. CI trong `.github/workflows/ci.yml` định nghĩa PostgreSQL/Redis services, Rust checks và container build; `.github/workflows/security.yml` chạy secret scanning. README này không khẳng định lần chạy CI mới nhất đã xanh; kiểm tra GitHub Actions trước khi merge/release.

## Các việc chưa được khẳng định là hoàn tất

- Gửi email qua provider production.
- Internal API để các service khác kiểm tra active session hoặc lấy verified primary email một cách đáng tin cậy.
- Revocation reconciliation cho access-token deny-list liên service.
- Tính đúng đắn end-to-end của từng flow dưới concurrency, lỗi hạ tầng và production configuration.
- Backup/restore, key rotation diễn tập, incident response và production deployment hardening.

## Production readiness

Không đánh dấu Identity Service production-ready chỉ vì code biên dịch hoặc test unit pass. Trước release cần bằng chứng thực tế về migration từ DB sạch và bản cũ, test concurrency/replay/failure trên PostgreSQL/Redis disposable, mail delivery thật, rotation/backup khóa, session revocation, secret management, logs/alerts, backup/restore, rollback và quy trình incident response. Xem thêm [`docs/operations.md`](docs/operations.md).
