# Bekratos

Bekratos là nền tảng hướng tới việc giúp người dùng tạo và vận hành backend bằng cấu hình no-code/low-code, đồng thời cho phép developer tích hợp API và tùy chỉnh hệ thống qua code.

> **Trạng thái hiện tại (26/10/2026):** `services/identity/` có source Rust, migration PostgreSQL, Docker, CI và integration/security tests. `services/control-plane/` và Backend Runtime/Data Plane chưa được triển khai trong repository. Việc có source/tests không đồng nghĩa toàn bộ test đã pass hoặc service đã production-ready; kiểm tra CI và xác minh trên môi trường disposable trước khi release.

## Kiến trúc dự kiến

- **Identity Service** — quản lý tài khoản nền tảng, credentials, sessions và platform JWT/JWKS cho người dùng đăng nhập Dashboard.
- **Control Plane (Go, kế hoạch)** — quản lý organizations, memberships, projects, environments, module registry/configuration, entity/field/relationship definitions, schema versions, API-key lifecycle và deployment state.
- **Backend Runtime / Data Plane (kế hoạch)** — phục vụ API của ứng dụng do khách hàng tạo; thực thi module contracts, Auth cho end-users, CRUD, validation, relationships và data authorization.

Platform users và project end-users là hai identity domains riêng. Platform JWT dùng cho management API; project credentials dùng cho runtime API. Không dùng API key của ứng dụng để gọi Control Plane, và không mặc định xem platform JWT là token của end-user.

## Repository layout

```text
bekratos/
├── .github/workflows/      # CI và security workflows
├── docs/                   # Tài liệu vận hành và thiết kế
└── services/
    └── identity/           # Rust, Axum, PostgreSQL, Redis
```

Khi Control Plane bắt đầu được triển khai, nó sẽ nằm trong `services/control-plane/`. Backend Runtime sẽ được đặt trong một service riêng sau khi domain và security contracts được chốt.

## Identity Service

Xem [Identity Service README](services/identity/README.md) để biết phạm vi API, mô hình dữ liệu, cấu hình, test và các hạn chế tích hợp.

Identity dùng PostgreSQL làm nguồn sự thật cho tài khoản và trạng thái bảo mật; Redis hỗ trợ rate limiting và các chức năng phụ trợ. Access token dùng JWT ES256/JWKS; refresh token được rotation. Endpoint JWKS hiện được đăng ký tại `/v1/jwks`.

Lưu ý trước khi dùng với người dùng thật: cấu hình mail hiện có `ConsoleMailer` không gửi email ra ngoài. Cần tích hợp và kiểm thử một mail provider thực, đồng thời xác minh CI, migration, secret handling, failure modes và deploy controls trước production.

## Control Plane design

Tài liệu thiết kế ban đầu dự kiến đặt trong `docs/control-plane/` sau khi được đưa vào repository. Các quyết định chính cho MVP:

- Shared runtime là mặc định; dedicated runtime là hướng mở rộng.
- Built-in/module entities giữ các field cốt lõi có kiểu dữ liệu thật; custom scalar fields có thể dùng JSONB.
- Custom entities dùng record store JSONB ở MVP; limitations về unique/index/query phải được ghi rõ.
- Relationships là định nghĩa có kiểu riêng, có cardinality, dependency và deletion behavior; không coi ID nằm trong JSONB là quan hệ đã được đảm bảo toàn vẹn.
- Schema/config được version hóa. Thay đổi tương thích có thể auto-apply; thay đổi phá vỡ tương thích hoặc destructive cần kiểm tra, xác nhận và migration an toàn.
- API keys chỉ nhận diện credential; authorization vẫn cần scope, role/policy và project/environment isolation.

## Development workflow

- Thực hiện mỗi phase trên branch riêng từ nhánh tích hợp đã thống nhất; không sửa `main` trực tiếp.
- Làm một phase mỗi lần, kiểm tra diff, chạy các test liên quan và dừng chờ review trước phase kế tiếp.
- Báo cáo kết quả kiểm tra thực tế; không tuyên bố test, commit, push hoặc deploy nếu chưa thực hiện.
- Không ghi secrets, password, token, magic links, private keys hoặc authorization headers vào source, fixtures, logs hay CI artifacts.

## Project status

- [x] Identity service source và tài liệu dịch vụ có trong repository.
- [ ] Xác minh CI hiện tại và đầy đủ test chạy trên PostgreSQL/Redis disposable.
- [ ] Chốt hợp đồng Identity ↔ Control Plane cho active-session validation và verified-primary-email; không giả định JWT hiện có các claim này.
- [ ] Implement Control Plane foundation.
- [ ] Implement module/schema configuration và safe activation/versioning.
- [ ] Implement Backend Runtime/Data Plane, tenant isolation và runtime authorization.
- [ ] Hoàn thành release-readiness/security review trước production.
