use p256::ecdsa::SigningKey;
use p256::elliptic_curve::sec1::ToEncodedPoint;
use p256::pkcs8::{EncodePrivateKey, EncodePublicKey, LineEnding};
use p256::PublicKey;
use rand::rngs::OsRng;
use serde_json::{json, Value as JsonValue};
use uuid::Uuid;

use crate::error::{AppError, AppResult};
use crate::infra::key_crypto::KeyEncryptor;
use crate::modules::jwks::repo::{JwksRepo, NewSigningKey, SigningKeyRow};
use crate::state::AppState;

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

pub async fn ensure_active_key(state: &AppState) -> AppResult<SigningKeyRow> {
    if let Some(active) = JwksRepo::find_active(&state.db).await? {
        return Ok(active);
    }
    create_and_activate_key(state).await
}

pub async fn create_and_activate_key(state: &AppState) -> AppResult<SigningKeyRow> {
    let encryptor = KeyEncryptor::new(&state.config.jwt_key_encryption_key)?;
    let version = JwksRepo::next_version(&state.db).await?;
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
    let mut tx = state.db.begin().await.map_err(|_| AppError::Internal)?;

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

    JwksRepo::find_by_kid(&state.db, &kid)
        .await?
        .ok_or(AppError::Internal)
}

pub async fn rotate_key(state: &AppState) -> AppResult<SigningKeyRow> {
    create_and_activate_key(state).await
}

pub fn decrypt_private_pem(encryptor: &KeyEncryptor, row: &SigningKeyRow) -> AppResult<String> {
    let plain = encryptor.decrypt(&row.private_key_enc)?;
    String::from_utf8(plain).map_err(|_| AppError::Internal)
}

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
