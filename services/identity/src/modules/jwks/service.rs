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

/// Trả JWKS của các khóa active/retiring còn trong thời gian công bố,
/// bổ sung `kid`, `use` và `alg`; lỗi truy vấn được truyền thành `Internal`.
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

/// Trả khóa active hiện có, hoặc tạo và kích hoạt khóa mới khi chưa có.
///
/// Không giải mã hay xác minh khả năng sử dụng khóa hiện có. Truyền `Internal`
/// từ truy vấn/tạo khóa và `Conflict` nếu không kích hoạt được khóa mới.
pub async fn ensure_active_key(
    pool: &PgPool,
    key_encryption_key: &str,
) -> AppResult<SigningKeyRow> {
    if let Some(active) = JwksRepo::find_active(pool).await? {
        return Ok(active);
    }
    create_and_activate_key(pool, key_encryption_key).await
}

/// Tạo khóa ES256 mới, lưu khóa riêng đã mã hóa và kích hoạt trong database.
///
/// Khóa active cũ chuyển sang retiring và được công bố thêm một giờ. Trả bản ghi
/// khóa mới sau commit; lỗi mã hóa, database hoặc đọc lại trả `Internal`,
/// không kích hoạt được bản ghi trả `Conflict`.
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

/// Tạo và kích hoạt khóa mới trong database bằng khóa mã hóa từ cấu hình.
///
/// Trả bản ghi khóa mới hoặc truyền `Internal`/`Conflict` từ quá trình tạo khóa.
/// Hàm không thay bộ ký hay tập khóa xác minh đang có trong `AppState`.
pub async fn rotate_key(state: &AppState) -> AppResult<SigningKeyRow> {
    create_and_activate_key(&state.db, &state.config.jwt_key_encryption_key).await
}

/// Giải mã khóa riêng lưu trong bản ghi, hỗ trợ cơ chế dẫn xuất khóa cũ.
///
/// Trả `Internal` nếu giải mã thất bại hoặc nội dung không phải UTF-8;
/// hàm không kiểm tra nội dung đã giải mã có phải PEM hợp lệ hay không.
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

/// Chuyển khóa công khai P-256 dạng PEM thành JWK dùng cho chữ ký ES256.
///
/// Trả `Internal` khi không đọc được khóa hoặc không lấy được tọa độ công khai.
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

fn base64_url_encode(data: &[u8]) -> String {
    use base64::engine::general_purpose::URL_SAFE_NO_PAD;
    use base64::Engine;
    URL_SAFE_NO_PAD.encode(data)
}
