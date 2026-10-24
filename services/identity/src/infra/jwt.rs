use std::collections::HashMap;
use std::sync::{Arc, RwLock};

use chrono::{Duration as ChronoDuration, Utc};
use jsonwebtoken::{
    decode, decode_header, encode, Algorithm, DecodingKey, EncodingKey, Header, Validation,
};
use p256::pkcs8::{DecodePrivateKey, EncodePrivateKey, EncodePublicKey, LineEnding};
use p256::SecretKey;
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::error::{AppError, AppResult};

#[derive(Debug, Serialize, Deserialize, Clone)]
pub struct AccessClaims {
    pub sub: String,
    pub sid: String,
    pub iss: String,
    pub aud: String,
    pub iat: i64,
    pub nbf: i64,
    pub exp: i64,
}

#[derive(Clone)]
pub struct JwtSigner {
    encoding_key: EncodingKey,
    kid: String,
    issuer: String,
    audience: String,
    access_ttl_secs: i64,
}

impl std::fmt::Debug for JwtSigner {
    /// Định dạng thông tin bộ ký để gỡ lỗi, không đưa khóa ký vào kết quả.
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("JwtSigner")
            .field("kid", &self.kid)
            .field("issuer", &self.issuer)
            .field("audience", &self.audience)
            .field("access_ttl_secs", &self.access_ttl_secs)
            .finish_non_exhaustive()
    }
}

impl JwtSigner {
    /// Tạo bộ ký ES256 từ khóa riêng P-256 dạng PEM PKCS#8.
    ///
    /// `access_ttl_secs` là thời hạn token tính bằng giây và phải dương. Trả `Config`
    /// khi thời hạn không hợp lệ, `kid`/issuer/audience trống hoặc khóa không dùng được.
    pub fn from_pem(
        private_pem: &str,
        kid: String,
        issuer: String,
        audience: String,
        access_ttl_secs: i64,
    ) -> AppResult<Self> {
        if access_ttl_secs <= 0 {
            return Err(AppError::config("JWT_ACCESS_TTL_SECS must be > 0"));
        }
        if kid.trim().is_empty() || issuer.trim().is_empty() || audience.trim().is_empty() {
            return Err(AppError::config("JWT kid/iss/aud must not be empty"));
        }

        let secret = SecretKey::from_pkcs8_pem(private_pem).map_err(|error| {
            tracing::error!(error = %error, "invalid JWT signing PEM");
            AppError::config("JWT signing PEM is not a valid PKCS8 P-256 private key")
        })?;
        let canonical_pem = secret.to_pkcs8_pem(LineEnding::LF).map_err(|error| {
            tracing::error!(error = %error, "failed to encode JWT signing key");
            AppError::config("failed to encode JWT signing key")
        })?;
        let encoding_key = EncodingKey::from_ec_pem(canonical_pem.as_bytes()).map_err(|error| {
            tracing::error!(error = %error, "failed to initialize ES256 signing key");
            AppError::config("JWT signing PEM cannot be used for ES256")
        })?;

        Ok(Self {
            encoding_key,
            kid,
            issuer,
            audience,
            access_ttl_secs,
        })
    }

    /// Ký access token ES256 chứa định danh người dùng và phiên đăng nhập.
    ///
    /// Token có hiệu lực từ thời điểm phát hành và hết hạn theo TTL đã cấu hình;
    /// trả `Internal` nếu ký thất bại. Hàm không tạo hay kiểm tra phiên trong database.
    pub fn issue_access_token(&self, user_id: Uuid, session_id: Uuid) -> AppResult<String> {
        let now = Utc::now();
        let issued_at = now.timestamp();
        let expires_at = (now + ChronoDuration::seconds(self.access_ttl_secs)).timestamp();
        let claims = AccessClaims {
            sub: user_id.to_string(),
            sid: session_id.to_string(),
            iss: self.issuer.clone(),
            aud: self.audience.clone(),
            iat: issued_at,
            nbf: issued_at,
            exp: expires_at,
        };

        let mut header = Header::new(Algorithm::ES256);
        header.kid = Some(self.kid.clone());

        encode(&header, &claims, &self.encoding_key).map_err(|error| {
            tracing::error!(error = %error, kid = %self.kid, "failed to sign access token");
            AppError::Internal
        })
    }

    /// Trả thời hạn access token tính bằng giây kể từ lúc phát hành.
    pub fn access_ttl_secs(&self) -> i64 {
        self.access_ttl_secs
    }

    pub fn kid(&self) -> &str {
        &self.kid
    }

    pub fn issuer(&self) -> &str {
        &self.issuer
    }

    pub fn audience(&self) -> &str {
        &self.audience
    }
}

#[derive(Clone)]
pub struct JwtVerifier {
    keys: Arc<RwLock<HashMap<String, DecodingKey>>>,
    issuer: String,
    audience: String,
}

impl std::fmt::Debug for JwtVerifier {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("JwtVerifier")
            .field("issuer", &self.issuer)
            .field("audience", &self.audience)
            .finish_non_exhaustive()
    }
}

impl JwtVerifier {
    /// Tạo bộ xác minh với issuer, audience và tập khóa ban đầu rỗng.
    ///
    /// Các bản sao dùng chung tập khóa xác minh.
    pub fn new(issuer: String, audience: String) -> Self {
        Self {
            keys: Arc::new(RwLock::new(HashMap::new())),
            issuer,
            audience,
        }
    }

    /// Nạp khóa công khai PEM để xác minh token; thay khóa cũ có cùng `kid`.
    ///
    /// Trả `Internal` khi không đọc được PEM hoặc khóa đồng bộ hóa đã bị nhiễm lỗi.
    pub async fn insert_key(&self, kid: String, public_pem: &str) -> AppResult<()> {
        let decoding_key = DecodingKey::from_ec_pem(public_pem.as_bytes()).map_err(|error| {
            tracing::error!(error = %error, %kid, "invalid JWT verification PEM");
            AppError::Internal
        })?;
        self.insert_decoding_key(kid, decoding_key).await
    }

    /// Thêm hoặc thay khóa xác minh theo `kid` trong tập khóa dùng chung.
    ///
    /// Trả `Internal` nếu khóa đồng bộ hóa đã bị nhiễm lỗi.
    pub async fn insert_decoding_key(&self, kid: String, key: DecodingKey) -> AppResult<()> {
        let mut keys = self.keys.write().map_err(|error| {
            tracing::error!(error = %error, "JWT verification key lock poisoned");
            AppError::Internal
        })?;
        keys.insert(kid, key);
        Ok(())
    }

    /// Loại khóa xác minh theo `kid`; khóa không tồn tại vẫn thành công.
    ///
    /// Trả `Internal` nếu khóa đồng bộ hóa đã bị nhiễm lỗi.
    pub async fn remove_key(&self, kid: &str) -> AppResult<()> {
        let mut keys = self.keys.write().map_err(|error| {
            tracing::error!(error = %error, "JWT verification key lock poisoned");
            AppError::Internal
        })?;
        keys.remove(kid);
        Ok(())
    }

    /// Xóa toàn bộ khóa xác minh dùng chung giữa các bản sao.
    ///
    /// Trả `Internal` nếu khóa đồng bộ hóa đã bị nhiễm lỗi.
    pub async fn clear(&self) -> AppResult<()> {
        let mut keys = self.keys.write().map_err(|error| {
            tracing::error!(error = %error, "JWT verification key lock poisoned");
            AppError::Internal
        })?;
        keys.clear();
        Ok(())
    }

    /// Xác minh ES256, `kid`, issuer, audience, thời hạn và thời điểm bắt đầu hiệu lực.
    ///
    /// Áp dụng dung sai thời gian mặc định của `jsonwebtoken`. Trả claims khi hợp lệ,
    /// `Unauthorized` khi token không hợp lệ hoặc không có khóa tương ứng, và `Internal`
    /// khi khóa đồng bộ hóa bị nhiễm lỗi. Không kiểm tra trạng thái phiên hay tài khoản.
    pub fn verify_access_token(&self, token: &str) -> AppResult<AccessClaims> {
        let header = decode_header(token).map_err(|_| AppError::Unauthorized)?;
        if header.alg != Algorithm::ES256 {
            return Err(AppError::Unauthorized);
        }
        let kid = header.kid.ok_or(AppError::Unauthorized)?;

        let keys = self.keys.read().map_err(|error| {
            tracing::error!(error = %error, "JWT verification key lock poisoned");
            AppError::Internal
        })?;
        let decoding_key = keys.get(&kid).ok_or(AppError::Unauthorized)?;

        let mut validation = Validation::new(Algorithm::ES256);
        validation.algorithms = vec![Algorithm::ES256];
        validation.set_issuer(&[self.issuer.as_str()]);
        validation.set_audience(&[self.audience.as_str()]);
        validation.validate_exp = true;
        validation.validate_nbf = true;

        let decoded = decode::<AccessClaims>(token, decoding_key, &validation)
            .map_err(|_| AppError::Unauthorized)?;
        Ok(decoded.claims)
    }

    /// Cung cấp giao diện bất đồng bộ cho `verify_access_token` với cùng kết quả
    /// và lỗi; thao tác xác minh vẫn chạy đồng bộ.
    pub async fn verify_access_token_async(&self, token: &str) -> AppResult<AccessClaims> {
        self.verify_access_token(token)
    }
}

/// Trích khóa công khai PEM từ khóa riêng P-256 dạng PEM PKCS#8.
///
/// Trả `Config` khi khóa riêng không hợp lệ và `Internal` nếu mã hóa PEM thất bại.
pub fn public_pem_from_private_pem(private_pem: &str) -> AppResult<String> {
    let secret = SecretKey::from_pkcs8_pem(private_pem).map_err(|error| {
        tracing::error!(error = %error, "invalid JWT signing PEM while deriving public key");
        AppError::config("JWT signing PEM is not a valid PKCS8 P-256 private key")
    })?;
    secret
        .public_key()
        .to_public_key_pem(LineEnding::LF)
        .map_err(|error| {
            tracing::error!(error = %error, "failed to encode JWT public key");
            AppError::Internal
        })
}
