# Bekratos

Bekratos là nền tảng Backend-as-a-Service hướng tới no-code/low-code. Người dùng có thể tạo backend bằng cách chọn modules, tùy chỉnh entities, fields và relationships qua Dashboard; developer có thể tích hợp API trực tiếp và tùy chỉnh ứng dụng bằng code.

> Trạng thái tại 27/10/2026: Identity có provider email Resend qua HTTPS và Control Plane có xác minh phiên trực tiếp qua Identity cùng HTTP provider adapter cho Runtime. Backend Runtime/Data Plane thật vẫn chưa có trong repository; vì vậy chưa có triển khai end-to-end. Mọi thay đổi trong PR #34 cần CI xanh và review; source code không tự chứng minh production readiness.

## Kiến trúc dự kiến

- Identity Service: tài khoản nền tảng, credentials, sessions và platform JWT/JWKS cho người đăng nhập Dashboard.
- Control Plane: organizations, memberships, projects, environments, module registry/configuration, entity/field/relationship definitions, configuration versions, API-key lifecycle và deployment state.
- Backend Runtime/Data Plane: API của ứng dụng do khách hàng tạo, runtime Auth dành cho end-users, CRUD, validation, relationships và data authorization.

Platform users và project end-users là hai identity domains riêng. Platform JWT dùng cho management API; runtime credentials và end-user tokens chỉ thuộc project/environment tương ứng. Không dùng runtime API key để gọi Control Plane, và không mặc định xem platform JWT là token của end-user.

## Repository

- services/identity/: Rust, Axum, PostgreSQL, Redis và JWT ES256/JWKS.
- docs/operations.md: vận hành Identity.
- docs/control-plane/domain.md: domain model và architecture boundary.
- docs/control-plane/implementation-plan.md: roadmap từng phase.
- docs/grok/CONTROL_PLANE_PROMPTS.md: prompt implementer/reviewer dùng lâu dài.
- docs/grok/CONTROL_PLANE_PHASE_1.md: prompt triển khai nền Go đầu tiên.
- services/control-plane/: sẽ được tạo trong Phase 1, chưa phải source hiện có.

## Identity Service

Xem [Identity Service README](services/identity/README.md) để biết route, cấu hình, mô hình dữ liệu và hạn chế tích hợp. Identity dùng PostgreSQL làm nguồn sự thật cho identity/security state; Redis hiện hỗ trợ rate limiting và chức năng phụ trợ. Access token dùng JWT ES256/JWKS; endpoint JWKS hiện là GET /v1/jwks.

Lưu ý trước khi dùng với người dùng thật: cấu hình ConsoleMailer hiện không gửi email ra ngoài. Cần tích hợp và kiểm thử mail provider thực, đồng thời xác minh CI, migrations, secret handling, failure modes và deploy controls trước production.

## Quyết định Control Plane MVP

- Shared runtime là mặc định; dedicated runtime là hướng mở rộng.
- Built-in/module entities giữ các field lõi có kiểu dữ liệu thật; custom scalar fields có thể lưu JSONB.
- Custom entities dùng record store JSONB trong MVP; giới hạn về unique/index/query phải được ghi rõ.
- Relationships là định nghĩa có kiểu riêng với cardinality, dependency và deletion behavior; không coi ID trong JSONB là quan hệ đã đảm bảo toàn vẹn.
- Config được version hóa. Thay đổi tương thích có thể auto-apply; thay đổi phá vỡ hoặc destructive cần xác nhận và migration an toàn.
- API keys chỉ nhận diện credential. Authorization vẫn cần role/policy, scope và project/environment isolation.
- Không chạy arbitrary user SQL, code hoặc eval trong runtime MVP.
- Không báo deployment ready khi chưa có xác nhận readiness thật từ Runtime/provider.

## Workflow

- Dùng dev làm integration branch; mỗi phase làm trên branch riêng từ dev mới nhất. Không sửa main trực tiếp.
- Implement một phase, review diff, chạy tests thực tế và dừng chờ review trước phase tiếp theo.
- Báo cáo trung thực lệnh đã chạy/chưa chạy. Không ghi secrets, passwords, tokens, magic links, private keys hoặc Authorization headers vào source, fixtures, logs hay CI artifacts.
- Trước production phải có cross-tenant authorization tests, migrations tests, failure/concurrency tests, observability, backup/restore và incident response đã được xác minh.
