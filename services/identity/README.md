# Bekratos Identity Service

Identity Service là microservice Rust quản lý tài khoản nền tảng Bekratos: đăng ký, xác minh email, đăng nhập, session, credential, profile và vòng đời tài khoản. Service dùng PostgreSQL làm nguồn sự thật cho identity/security state, Redis cho rate limiting, và ES256 JWT/JWKS cho access token.

> **Phạm vi repo hiện tại:** source tree được kiểm tra trên branch `dev` ngày 2026-10-26 có implementation của `services/identity/`, migration, Docker, CI và integration/security tests. Đây không còn chỉ là repo bootstrap/Phase 0. Tuy nhiên, việc các route tồn tại và tests có trong repo không chứng minh rằng tất cả đã chạy thành công hoặc service đã production-ready. Kiểm tra trạng thái CI thực tế trước khi release.

Control Plane là service tiếp theo trong roadmap; tại thời điểm kiểm tra, `services/control-plane/` chưa có trong repository tree. Control Plane sẽ quản lý organizations/projects/modules/configuration/API-key lifecycle; Identity vẫn là nguồn sự thật cho tài khoản và session của người đăng nhập Dashboard.

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

Cấu hình mail hiện được thể hiện dưới dạng `MAIL_PROVIDER=console|none`. `ConsoleMailer` chỉ ghi metadata email vào log và trả thành công; nó **không gửi email ra ngoài**. `NoopMailer` trả lỗi unavailable. Vì vậy, trước khi dùng luồng xác minh/reset/restore cho người dùng thật, cần tích hợp một mail provider thực, có retry/backoff, timeout, quan sát lỗi và chính sách bảo vệ dữ liệu. Không coi console mailer là triển khai production.

## Tích hợp Control Plane — hợp đồng cần chốt

Control Plane dùng Identity để xác thực người đăng nhập Dashboard; project end-users là danh tính khác, thuộc Auth module của runtime theo từng project/environment.

Trong source Identity được kiểm tra ngày 2026-10-26:

- Access JWT có các claim cơ bản `sub`, `sid`, `iss`, `aud`, `iat`, `nbf`, `exp`.
- JWKS route là `GET /v1/jwks`.
- Chưa thấy route chuyên dụng cho service khác kiểm tra trạng thái session còn hiệu lực.
- Không nên giả định JWT hiện chứa verified primary email.

Trước khi mở Control Plane cho thao tác quản trị nhạy cảm, cần thiết kế một hợp đồng service-to-service được xác thực để kiểm tra active session và lấy verified-primary-email, hoặc ghi nhận rõ chính sách yếu hơn với thời gian thu hồi tối đa. Không tin `X-User-ID`, email hoặc role do caller tự truyền.

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

**Email:** local `MAIL_PROVIDER=console` không gửi email. Đừng coi tài khoản đăng ký là có thể nhận magic link cho đến khi mail provider thật hoặc một quy trình test token an toàn được cấu hình.

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
