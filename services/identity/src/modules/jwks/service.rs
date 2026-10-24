use p256::ecdsa::SigningKey;
use p256::elliptic_curve::sec1::ToEncodedPoint;
use p256::pkcs8::{EncodePrivateKey, EncodePublicKey, LineEnding};
use p256::PublicKey;
use rand::rngs::OsRng;
use serde_json::{json, Value as JsonValue};
use sqlx::PgPool;
use uuid::Uuid;

use crate::error::{AppError, AppResult};
use crate::infra::key_crypto::KeyEncryptor;
use crate::modules::jwks::repo::{JwksRepo, NewSigningKey, SigningKeyRow};
use crate::state::AppState;

/// Trả tài liệu JWKS chứa các khóa được phép công bố, từ phiên bản mới nhất trước.
/// Bổ sung `kid`, mục đích ký và thuật toán ES256 cho JWK dạng đối tượng;
/// lỗi truy vấn được truyền ra dưới dạng `Internal`.
pub async fn get_jwks(state: &AppState) -> AppResult<JsonValue> {
    let keys = JwksRepo::list_publishable(&state.db).await?;
    let jwks: Vec<JsonValue> = keys
        .into_iter()
        .map(|k| {
            let mut jwk = k.public_jwk;
            if let Some(obj) = jwk.as_object_mut() {
                obj.insert("kid".into(), json!(k.kid));
                obj.insert("use".into(), json!("sig"));
                obj.insert("alg".into(), json!("ES256"));
            }
            jwk
        })
        .collect();
    Ok(json!({ "keys": jwks }))
}

/// Trả khóa active đang có, hoặc tạo và kích hoạt khóa mới nếu chưa có.
/// Truyền tiếp lỗi `Internal` hoặc `Conflict` từ truy vấn và khởi tạo khóa.
pub async fn ensure_active_key(
    pool: &PgPool,
    key_encryption_key: &str,
) -> AppResult<SigningKeyRow> {
    if let Some(active) = JwksRepo::find_active(pool).await? {
        return Ok(active);
    }
    create_and_activate_key(pool, key_encryption_key).await
}

/// Tạo khóa P-256, lưu khóa riêng được mã hóa bằng KEK và kích hoạt khóa trong PostgreSQL.
/// Khóa active trước đó được giữ công bố thêm một giờ. Trả bản ghi khóa mới;
/// lỗi mã hóa, chuyển định dạng hoặc cơ sở dữ liệu trả `Internal`, lỗi kích hoạt
/// truyền tiếp `Conflict`. Không cập nhật bộ ký hoặc bộ xác thực đang chạy trong bộ nhớ.
pub async fn create_and_activate_key(
    pool: &PgPool,
    key_encryption_key: &str,
) -> AppResult<SigningKeyRow> {
    let encryptor = KeyEncryptor::new(key_encryption_key)?;
    let version = JwksRepo::next_version(pool).await?;
    let kid = format!("es256-{}", version);

    let signing_key = SigningKey::random(&mut OsRng);
    let private_pem = signing_key
        .to_pkcs8_pem(LineEnding::LF)
        .map_err(|_| AppError::Internal)?;
    let public_key: PublicKey = signing_key.verifying_key().into();
    let public_pem = public_key
        .to_public_key_pem(LineEnding::LF)
        .map_err(|_| AppError::Internal)?;

    let private_enc = encryptor.encrypt(private_pem.as_bytes())?;
    let public_jwk = pem_public_to_jwk(&public_pem, &kid)?;

    let id = Uuid::now_v7();
    let mut tx = pool.begin().await.map_err(|_| AppError::Internal)?;

    JwksRepo::insert_tx(
        &mut tx,
        NewSigningKey {
            id,
            kid: kid.clone(),
            public_jwk,
            private_key_enc: private_enc,
            key_version: version,
            status: "pending".to_string(),
            publish_at: Some(chrono::Utc::now()),
        },
    )
    .await?;

    JwksRepo::activate_tx(&mut tx, id).await?;

    tx.commit().await.map_err(|_| AppError::Internal)?;

    JwksRepo::find_by_kid(pool, &kid)
        .await?
        .ok_or(AppError::Internal)
}

/// Tạo và kích hoạt khóa mới trong PostgreSQL bằng KEK cấu hình, trả bản ghi khóa.
/// Không thay bộ ký hay bộ xác thực trong `state`; truyền tiếp `Internal` và `Conflict`.
pub async fn rotate_key(state: &AppState) -> AppResult<SigningKeyRow> {
    create_and_activate_key(&state.db, &state.config.jwt_key_encryption_key).await
}

/// Giải mã khóa riêng đã lưu, hỗ trợ cách suy KEK hiện tại và cũ, trả chuỗi UTF-8.
/// Trả `Internal` nếu giải mã thất bại hoặc bản rõ không phải UTF-8;
/// không kiểm tra cấu trúc PEM tại đây.
pub fn decrypt_private_pem(encryptor: &KeyEncryptor, row: &SigningKeyRow) -> AppResult<String> {
    let (plain, was_legacy) = encryptor.decrypt_and_was_legacy(&row.private_key_enc)?;
    if was_legacy {
        tracing::warn!(
            kid = %row.kid,
            "decrypted signing key with legacy KEK derivation; rotate or re-encrypt when convenient"
        );
    }
    String::from_utf8(plain).map_err(|_| AppError::Internal)
}

/// Chuyển PEM công khai P-256 sang JWK dành cho chữ ký ES256 với `kid` được cấp.
/// Trả `Internal` nếu PEM không đọc được hoặc không lấy được tọa độ khóa.
fn pem_public_to_jwk(pem: &str, kid: &str) -> AppResult<JsonValue> {
    use p256::pkcs8::DecodePublicKey;
    let public = PublicKey::from_public_key_pem(pem).map_err(|_| AppError::Internal)?;
    let encoded = public.to_encoded_point(false);
    let x = encoded.x().ok_or(AppError::Internal)?;
    let y = encoded.y().ok_or(AppError::Internal)?;
    let x_b64 = base64_url_encode(x);
    let y_b64 = base64_url_encode(y);
    Ok(json!({
        "kty": "EC",
        "crv": "P-256",
        "x": x_b64,
        "y": y_b64,
        "kid": kid,
        "use": "sig",
        "alg": "ES256"
    }))
}

/// Mã hóa dữ liệu thành base64url không padding cho tọa độ JWK.
fn base64_url_encode(data: &[u8]) -> String {
    use base64::engine::general_purpose::URL_SAFE_NO_PAD;
    use base64::Engine;
    URL_SAFE_NO_PAD.encode(data)
}
