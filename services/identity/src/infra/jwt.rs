use chrono::{Duration as ChronoDuration, Utc};
use jsonwebtoken::{encode, Algorithm, EncodingKey, Header};
use p256::pkcs8::DecodePrivateKey;
use p256::pkcs8::EncodePrivateKey;
use p256::SecretKey;
use serde::{Deserialize, Serialize};
use uuid::Uuid;

use crate::error::{AppError, AppResult};

#[derive(Clone)]
pub struct JwtSigner {
    encoding_key: EncodingKey,
    kid: String,
    issuer: String,
    audience: String,
    access_ttl_secs: i64,
}

impl std::fmt::Debug for JwtSigner {
    fn fmt(&self, f: &mut std::fmt::Formatter<'_>) -> std::fmt::Result {
        f.debug_struct("JwtSigner")
            .field("kid", &self.kid)
            .field("issuer", &self.issuer)
            .field("audience", &self.audience)
            .field("access_ttl_secs", &self.access_ttl_secs)
            .finish_non_exhaustive()
    }
}

#[derive(Debug, Serialize, Deserialize)]
pub struct AccessClaims {
    pub sub: String,
    pub sid: String,
    pub iss: String,
    pub aud: String,
    pub iat: i64,
    pub nbf: i64,
    pub exp: i64,
}

impl JwtSigner {
    pub fn from_pem(
        pem: &str,
        kid: String,
        issuer: String,
        audience: String,
        access_ttl_secs: i64,
    ) -> AppResult<Self> {
        if access_ttl_secs <= 0 {
            return Err(AppError::config("JWT_ACCESS_TTL_SECS must be > 0"));
        }
        if kid.is_empty() || issuer.is_empty() || audience.is_empty() {
            return Err(AppError::config("JWT kid/iss/aud must not be empty"));
        }

        let secret = SecretKey::from_pkcs8_pem(pem).map_err(|e| {
            tracing::error!(error = %e, "invalid JWT signing PEM");
            AppError::config("JWT_SIGNING_KEY_PEM is not a valid PKCS8 EC private key")
        })?;

        let pkcs8 = secret
            .to_pkcs8_pem(p256::pkcs8::LineEnding::LF)
            .map_err(|_| AppError::config("failed to encode JWT signing key"))?;

        let encoding_key = EncodingKey::from_ec_pem(pkcs8.as_bytes()).map_err(|e| {
            tracing::error!(error = %e, "EncodingKey from PEM failed");
            AppError::config("JWT_SIGNING_KEY_PEM cannot be used for ES256")
        })?;

        Ok(Self {
            encoding_key,
            kid,
            issuer,
            audience,
            access_ttl_secs,
        })
    }

    pub fn issue_access_token(&self, user_id: Uuid, session_id: Uuid) -> AppResult<String> {
        let now = Utc::now();
        let exp = now + ChronoDuration::seconds(self.access_ttl_secs);
        let claims = AccessClaims {
            sub: user_id.to_string(),
            sid: session_id.to_string(),
            iss: self.issuer.clone(),
            aud: self.audience.clone(),
            iat: now.timestamp(),
            nbf: now.timestamp(),
            exp: exp.timestamp(),
        };

        let mut header = Header::new(Algorithm::ES256);
        header.kid = Some(self.kid.clone());

        encode(&header, &claims, &self.encoding_key).map_err(|e| {
            tracing::error!(error = %e, "jwt encode failed");
            AppError::Internal
        })
    }

    pub fn access_ttl_secs(&self) -> i64 {
        self.access_ttl_secs
    }

    pub fn kid(&self) -> &str {
        &self.kid
    }
}
