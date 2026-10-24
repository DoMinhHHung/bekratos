use identity::infra::jwt::{public_pem_from_private_pem, AccessClaims, JwtSigner, JwtVerifier};
use jsonwebtoken::{decode_header, encode, Algorithm, EncodingKey, Header};
use p256::ecdsa::SigningKey;
use p256::pkcs8::{EncodePrivateKey, EncodePublicKey, LineEnding};
use p256::PublicKey;
use rand::rngs::OsRng;
use uuid::Uuid;

fn make_signer(kid: &str, issuer: &str, audience: &str) -> (JwtSigner, String) {
    let key = SigningKey::random(&mut OsRng);
    let private_pem = key.to_pkcs8_pem(LineEnding::LF).expect("private PEM");
    let public_key: PublicKey = key.verifying_key().into();
    let public_pem = public_key
        .to_public_key_pem(LineEnding::LF)
        .expect("public PEM");
    let signer = JwtSigner::from_pem(
        &private_pem,
        kid.to_string(),
        issuer.to_string(),
        audience.to_string(),
        900,
    )
    .expect("signer");
    (signer, public_pem)
}

#[tokio::test]
async fn verifies_es256_access_token_claims_and_key_id() {
    let (signer, public_pem) = make_signer("key-1", "identity", "bekratos-api");
    let verifier = JwtVerifier::new("identity".into(), "bekratos-api".into());
    verifier
        .insert_key("key-1".into(), &public_pem)
        .await
        .expect("insert verification key");

    let user_id = Uuid::now_v7();
    let session_id = Uuid::now_v7();
    let token = signer
        .issue_access_token(user_id, session_id)
        .expect("issue token");
    let header = decode_header(&token).expect("decode JWT header");
    assert_eq!(header.alg, Algorithm::ES256);
    assert_eq!(header.kid.as_deref(), Some("key-1"));

    let claims = verifier
        .verify_access_token(&token)
        .expect("verify access token");
    assert_eq!(claims.sub, user_id.to_string());
    assert_eq!(claims.sid, session_id.to_string());
    assert_eq!(claims.iss, "identity");
    assert_eq!(claims.aud, "bekratos-api");
    assert!(claims.exp > claims.iat);
}

#[tokio::test]
async fn rejects_unknown_kid_wrong_audience_and_modified_signature() {
    let (signer, public_pem) = make_signer("known-key", "identity", "bekratos-api");
    let verifier = JwtVerifier::new("identity".into(), "bekratos-api".into());
    verifier
        .insert_key("known-key".into(), &public_pem)
        .await
        .expect("insert verification key");

    let token = signer
        .issue_access_token(Uuid::now_v7(), Uuid::now_v7())
        .expect("issue token");
    let unknown_key_verifier = JwtVerifier::new("identity".into(), "bekratos-api".into());
    assert!(unknown_key_verifier.verify_access_token(&token).is_err());

    let (wrong_audience_signer, wrong_audience_public) =
        make_signer("wrong-audience", "identity", "other-api");
    let wrong_audience_verifier = JwtVerifier::new("identity".into(), "bekratos-api".into());
    wrong_audience_verifier
        .insert_key("wrong-audience".into(), &wrong_audience_public)
        .await
        .expect("insert verification key");
    let wrong_audience_token = wrong_audience_signer
        .issue_access_token(Uuid::now_v7(), Uuid::now_v7())
        .expect("issue wrong-audience token");
    assert!(wrong_audience_verifier
        .verify_access_token(&wrong_audience_token)
        .is_err());

    let (wrong_issuer_signer, wrong_issuer_public) =
        make_signer("wrong-issuer", "other-issuer", "bekratos-api");
    verifier
        .insert_key("wrong-issuer".into(), &wrong_issuer_public)
        .await
        .expect("insert wrong-issuer key");
    let wrong_issuer_token = wrong_issuer_signer
        .issue_access_token(Uuid::now_v7(), Uuid::now_v7())
        .expect("issue wrong-issuer token");
    assert!(verifier.verify_access_token(&wrong_issuer_token).is_err());

    let mut parts = token.split('.').map(str::to_string).collect::<Vec<_>>();
    assert_eq!(parts.len(), 3);
    use base64::Engine;
    parts[2] = base64::engine::general_purpose::URL_SAFE_NO_PAD.encode([0_u8; 64]);
    assert!(verifier.verify_access_token(&parts.join(".")).is_err());
}

#[tokio::test]
async fn rejects_expired_access_token() {
    let key = SigningKey::random(&mut OsRng);
    let private_pem = key.to_pkcs8_pem(LineEnding::LF).expect("private PEM");
    let public_key: PublicKey = key.verifying_key().into();
    let public_pem = public_key
        .to_public_key_pem(LineEnding::LF)
        .expect("public PEM");
    let verifier = JwtVerifier::new("identity".into(), "bekratos-api".into());
    verifier
        .insert_key("expired-key".into(), &public_pem)
        .await
        .expect("insert verification key");

    let mut header = Header::new(Algorithm::ES256);
    header.kid = Some("expired-key".into());
    let claims = AccessClaims {
        sub: Uuid::now_v7().to_string(),
        sid: Uuid::now_v7().to_string(),
        iss: "identity".into(),
        aud: "bekratos-api".into(),
        iat: 1,
        nbf: 1,
        exp: 1,
    };
    let token = encode(
        &header,
        &claims,
        &EncodingKey::from_ec_pem(private_pem.as_bytes()).expect("encoding key"),
    )
    .expect("encode expired token");

    assert!(verifier.verify_access_token(&token).is_err());
}

#[tokio::test]
async fn rejects_non_es256_algorithms_and_tokens_not_yet_valid() {
    let key = SigningKey::random(&mut OsRng);
    let private_pem = key.to_pkcs8_pem(LineEnding::LF).expect("private PEM");
    let public_key: PublicKey = key.verifying_key().into();
    let public_pem = public_key
        .to_public_key_pem(LineEnding::LF)
        .expect("public PEM");
    let verifier = JwtVerifier::new("identity".into(), "bekratos-api".into());
    verifier
        .insert_key("strict-key".into(), &public_pem)
        .await
        .expect("insert verification key");

    let now = chrono::Utc::now().timestamp();
    let claims = AccessClaims {
        sub: Uuid::now_v7().to_string(),
        sid: Uuid::now_v7().to_string(),
        iss: "identity".into(),
        aud: "bekratos-api".into(),
        iat: now,
        nbf: now + 3600,
        exp: now + 7200,
    };
    let mut es256_header = Header::new(Algorithm::ES256);
    es256_header.kid = Some("strict-key".into());
    let not_yet_valid = encode(
        &es256_header,
        &claims,
        &EncodingKey::from_ec_pem(private_pem.as_bytes()).expect("ES256 encoding key"),
    )
    .expect("encode not-yet-valid token");
    assert!(verifier.verify_access_token(&not_yet_valid).is_err());

    let mut hmac_header = Header::new(Algorithm::HS256);
    hmac_header.kid = Some("strict-key".into());
    let hmac_token = encode(
        &hmac_header,
        &claims,
        &EncodingKey::from_secret(b"not-an-es256-key"),
    )
    .expect("encode HMAC token");
    assert!(verifier.verify_access_token(&hmac_token).is_err());
}

#[tokio::test]
async fn verifier_accepts_old_and_new_keys_during_rotation_until_old_key_is_removed() {
    let (old_signer, old_public_pem) = make_signer("old-key", "identity", "bekratos-api");
    let (new_signer, new_public_pem) = make_signer("new-key", "identity", "bekratos-api");
    let verifier = JwtVerifier::new("identity".into(), "bekratos-api".into());
    verifier
        .insert_key("old-key".into(), &old_public_pem)
        .await
        .expect("insert old key");

    let old_token = old_signer
        .issue_access_token(Uuid::now_v7(), Uuid::now_v7())
        .expect("issue with old key");
    verifier
        .insert_key("new-key".into(), &new_public_pem)
        .await
        .expect("insert new key");
    let new_token = new_signer
        .issue_access_token(Uuid::now_v7(), Uuid::now_v7())
        .expect("issue with new key");

    assert!(verifier.verify_access_token(&old_token).is_ok());
    assert!(verifier.verify_access_token(&new_token).is_ok());

    verifier
        .remove_key("old-key")
        .await
        .expect("remove old key");
    assert!(verifier.verify_access_token(&old_token).is_err());
    assert!(verifier.verify_access_token(&new_token).is_ok());
}

#[test]
fn signer_debug_and_public_key_derivation_do_not_expose_private_material() {
    let (signer, _) = make_signer("debug-key", "identity", "bekratos-api");
    let private_pem = SigningKey::random(&mut OsRng)
        .to_pkcs8_pem(LineEnding::LF)
        .expect("private PEM");
    let derived_public_pem = public_pem_from_private_pem(&private_pem).expect("derive public key");

    assert!(derived_public_pem.contains("BEGIN PUBLIC KEY"));
    let debug = format!("{signer:?}");
    assert!(debug.contains("debug-key"));
    assert!(!debug.contains("PRIVATE KEY"));
}
