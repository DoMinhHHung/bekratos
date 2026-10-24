use axum::extract::FromRequestParts;
use axum::http::request::Parts;
use uuid::Uuid;

use crate::error::AppError;
use crate::infra::jwt::AccessClaims;
use crate::modules::session::repo::SessionRepo;
use crate::modules::user::repo::UserRepo;
use crate::state::AppState;

#[derive(Debug, Clone)]
pub struct AuthUser {
    pub user_id: Uuid,
    pub session_id: Uuid,
    pub claims: AccessClaims,
}

impl FromRequestParts<AppState> for AuthUser {
    type Rejection = AppError;

    /// Lấy Bearer token và yêu cầu người dùng active cùng phiên đúng chủ sở hữu,
    /// chưa thu hồi và chưa hết hạn. Trả `Unauthorized` khi không đạt điều kiện xác thực;
    /// lỗi cơ sở dữ liệu hoặc khóa đồng bộ của bộ xác thực được truyền ra dưới dạng `Internal`.
    async fn from_request_parts(
        parts: &mut Parts,
        state: &AppState,
    ) -> Result<Self, Self::Rejection> {
        let value = parts
            .headers
            .get(axum::http::header::AUTHORIZATION)
            .and_then(|v| v.to_str().ok())
            .ok_or(AppError::Unauthorized)?;
        let token = value
            .strip_prefix("Bearer ")
            .or_else(|| value.strip_prefix("bearer "))
            .filter(|t| !t.is_empty())
            .ok_or(AppError::Unauthorized)?;

        let claims = state.jwt_verifier.verify_access_token(token)?;
        let user_id = Uuid::parse_str(&claims.sub).map_err(|_| AppError::Unauthorized)?;
        let session_id = Uuid::parse_str(&claims.sid).map_err(|_| AppError::Unauthorized)?;

        let session = SessionRepo::new(&state.db)
            .find_by_id(session_id)
            .await?
            .ok_or(AppError::Unauthorized)?;

        if session.user_id != user_id
            || session.revoked_at.is_some()
            || session.expires_at <= chrono::Utc::now()
        {
            return Err(AppError::Unauthorized);
        }

        let user = UserRepo::new(&state.db)
            .find_by_id(user_id)
            .await?
            .ok_or(AppError::Unauthorized)?;

        if user.status != "active" {
            return Err(AppError::Unauthorized);
        }

        Ok(AuthUser {
            user_id,
            session_id,
            claims,
        })
    }
}
