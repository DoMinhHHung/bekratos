use serde::Serialize;
use utoipa::openapi::security::{Http, HttpAuthScheme, SecurityScheme};
use utoipa::{Modify, OpenApi, ToSchema};
use utoipa_swagger_ui::SwaggerUi;

#[derive(Serialize, ToSchema)]
pub struct Jwk {
    pub kty: String,
    pub crv: String,
    pub x: String,
    pub y: String,
    pub kid: String,
    #[serde(rename = "use")]
    pub key_use: String,
    pub alg: String,
}

#[derive(Serialize, ToSchema)]
pub struct JwksDocument {
    pub keys: Vec<Jwk>,
}

#[derive(Serialize, ToSchema)]
pub struct ServiceStatus {
    pub status: String,
}

struct BearerAuth;

impl Modify for BearerAuth {
    /// Bổ sung sơ đồ bảo mật HTTP Bearer có tên `bearer_auth` vào tài liệu OpenAPI.
    fn modify(&self, openapi: &mut utoipa::openapi::OpenApi) {
        let components = openapi.components.get_or_insert_with(Default::default);
        components.add_security_scheme(
            "bearer_auth",
            SecurityScheme::Http(Http::new(HttpAuthScheme::Bearer)),
        );
    }
}

#[derive(OpenApi)]
#[openapi(
    info(
        title = "Bekratos Identity API",
        version = "1.0.0",
        description = "Identity, authentication, session, and public signing-key endpoints."
    ),
    paths(
        crate::app::healthz,
        crate::app::readyz,
        crate::modules::auth::routes::register,
        crate::modules::auth::routes::verify_email,
        crate::modules::auth::routes::resend_verification,
        crate::modules::auth::routes::login,
        crate::modules::auth::routes::logout,
        crate::modules::auth::routes::refresh,
        crate::modules::auth::routes::forgot_password,
        crate::modules::auth::routes::reset_password,
        crate::modules::auth::routes::change_password,
        crate::modules::auth::routes::request_restore,
        crate::modules::auth::routes::confirm_restore,
        crate::modules::user::routes::get_profile,
        crate::modules::user::routes::update_profile,
        crate::modules::user::routes::list_sessions,
        crate::modules::user::routes::revoke_one,
        crate::modules::user::routes::revoke_others,
        crate::modules::user::routes::delete_account,
        crate::modules::jwks::routes::jwks
    ),
    components(schemas(
        crate::modules::auth::dto::RegisterRequest,
        crate::modules::auth::dto::RegisterResponse,
        crate::modules::auth::dto::VerifyEmailRequest,
        crate::modules::auth::dto::VerifyEmailResponse,
        crate::modules::auth::dto::ResendVerificationRequest,
        crate::modules::auth::dto::ResendVerificationResponse,
        crate::modules::auth::dto::LoginRequest,
        crate::modules::auth::dto::LoginResponse,
        crate::modules::auth::dto::LogoutResponse,
        crate::modules::auth::dto::RefreshRequest,
        crate::modules::auth::dto::RefreshResponse,
        crate::modules::auth::dto::ForgotPasswordRequest,
        crate::modules::auth::dto::ForgotPasswordResponse,
        crate::modules::auth::dto::ResetPasswordRequest,
        crate::modules::auth::dto::ResetPasswordResponse,
        crate::modules::auth::dto::ChangePasswordRequest,
        crate::modules::auth::dto::ChangePasswordResponse,
        crate::modules::auth::dto::RestoreRequestBody,
        crate::modules::auth::dto::RestoreConfirmRequest,
        crate::modules::auth::dto::RestoreResponse,
        crate::modules::user::dto::ProfileResponse,
        crate::modules::user::dto::UpdateProfileRequest,
        crate::modules::user::dto::SessionItem,
        crate::modules::user::dto::SessionListResponse,
        crate::modules::user::dto::MessageResponse,
        Jwk,
        JwksDocument,
        ServiceStatus
    )),
    modifiers(&BearerAuth),
    tags(
        (name = "Health", description = "Liveness and readiness checks"),
        (name = "Authentication", description = "Registration, login, tokens, and account recovery"),
        (name = "Users", description = "User profile and account lifecycle"),
        (name = "Sessions", description = "Session management"),
        (name = "JWKS", description = "Public JSON Web Key Set")
    )
)]
pub struct ApiDoc;

/// Tạo Swagger UI tại `/swagger-ui` cùng tài liệu OpenAPI tại `/api-docs/openapi.json`.
pub fn swagger_ui() -> SwaggerUi {
    SwaggerUi::new("/swagger-ui").url("/api-docs/openapi.json", ApiDoc::openapi())
}

#[cfg(test)]
mod tests {
    use super::{swagger_ui, ApiDoc};
    use axum::body::Body;
    use axum::http::{Request, StatusCode};
    use axum::Router;
    use tower::ServiceExt;
    use utoipa::OpenApi;

    #[test]
    fn openapi_document_contains_all_api_routes_and_scoped_bearer_auth() {
        let document = serde_json::to_value(ApiDoc::openapi()).expect("serialize OpenAPI document");
        let paths = document["paths"].as_object().expect("OpenAPI paths");
        for path in [
            "/healthz",
            "/readyz",
            "/v1/auth/register",
            "/v1/auth/verify-email",
            "/v1/auth/resend-verification",
            "/v1/auth/login",
            "/v1/auth/logout",
            "/v1/auth/refresh",
            "/v1/auth/forgot-password",
            "/v1/auth/reset-password",
            "/v1/auth/change-password",
            "/v1/auth/request-restore",
            "/v1/auth/restore",
            "/v1/me",
            "/v1/me/sessions",
            "/v1/me/sessions/{id}",
            "/v1/me/delete",
            "/v1/jwks",
        ] {
            assert!(paths.contains_key(path), "OpenAPI route missing: {path}");
        }
        assert_eq!(paths.len(), 18);

        let security_schemes = &document["components"]["securitySchemes"];
        assert_eq!(security_schemes["bearer_auth"]["scheme"], "bearer");
        assert!(paths["/v1/me"]["get"]["security"]
            .as_array()
            .is_some_and(|entries| !entries.is_empty()));
        assert!(paths["/v1/auth/login"]["post"].get("security").is_none());
    }

    #[tokio::test]
    async fn swagger_ui_and_openapi_document_are_served() {
        let app = Router::new().merge(swagger_ui());

        let openapi_response = app
            .clone()
            .oneshot(
                Request::builder()
                    .uri("/api-docs/openapi.json")
                    .body(Body::empty())
                    .expect("build OpenAPI request"),
            )
            .await
            .expect("serve OpenAPI document");
        assert_eq!(openapi_response.status(), StatusCode::OK);
        let body = axum::body::to_bytes(openapi_response.into_body(), 1_000_000)
            .await
            .expect("read OpenAPI response");
        let document: serde_json::Value =
            serde_json::from_slice(&body).expect("parse served OpenAPI JSON");
        assert!(document["paths"]["/v1/auth/register"]["post"].is_object());

        let ui_response = app
            .oneshot(
                Request::builder()
                    .uri("/swagger-ui/")
                    .body(Body::empty())
                    .expect("build Swagger UI request"),
            )
            .await
            .expect("serve Swagger UI");
        assert_eq!(ui_response.status(), StatusCode::OK);
        assert!(ui_response
            .headers()
            .get(axum::http::header::CONTENT_TYPE)
            .is_some_and(|value| value
                .to_str()
                .is_ok_and(|value| value.contains("text/html"))));
    }
}
