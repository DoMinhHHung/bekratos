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

#[test]
fn signer_rejects_nonpositive_ttls_and_blank_identifiers() {
    let pem = SigningKey::random(&mut OsRng)
        .to_pkcs8_pem(LineEnding::LF)
        .unwrap();
    for (kid, issuer, audience, ttl) in [
        ("key", "identity", "api", 0),
        ("key", "identity", "api", -1),
        ("", "identity", "api", 900),
        (" \t", "identity", "api", 900),
        ("key", "\n", "api", 900),
        ("key", "identity", " ", 900),
    ] {
        assert!(matches!(
            JwtSigner::from_pem(&pem, kid.into(), issuer.into(), audience.into(), ttl),
            Err(identity::AppError::Config(_))
        ));
    }
}

#[test]
fn malformed_private_keys_are_configuration_errors() {
    for pem in [
        "",
        "not a PEM",
        "-----BEGIN PRIVATE KEY-----\ninvalid\n-----END PRIVATE KEY-----",
    ] {
        assert!(matches!(
            JwtSigner::from_pem(pem, "key".into(), "identity".into(), "api".into(), 900),
            Err(identity::AppError::Config(_))
        ));
        assert!(matches!(
            public_pem_from_private_pem(pem),
            Err(identity::AppError::Config(_))
        ));
    }
}

#[tokio::test]
async fn issued_claims_use_the_configured_ttl_and_current_time() {
    let (signer, public_pem) = make_signer("ttl-key", "identity", "bekratos-api");
    let verifier = JwtVerifier::new("identity".into(), "bekratos-api".into());
    verifier
        .insert_key("ttl-key".into(), &public_pem)
        .await
        .unwrap();
    let before = chrono::Utc::now().timestamp();
    let token = signer
        .issue_access_token(Uuid::now_v7(), Uuid::now_v7())
        .unwrap();
    let after = chrono::Utc::now().timestamp();
    let claims = verifier.verify_access_token_async(&token).await.unwrap();
    assert!((before..=after).contains(&claims.iat));
    assert_eq!(claims.nbf, claims.iat);
    assert_eq!(claims.exp - claims.iat, 900);
    assert_eq!(signer.access_ttl_secs(), 900);
    assert_eq!(signer.kid(), "ttl-key");
    assert_eq!(signer.issuer(), "identity");
    assert_eq!(signer.audience(), "bekratos-api");
}

#[tokio::test]
async fn clearing_a_cloned_verifier_revokes_all_shared_keys() {
    let verifier = JwtVerifier::new("identity".into(), "bekratos-api".into());
    let mut tokens = Vec::new();
    for kid in ["first", "second"] {
        let (signer, public_pem) = make_signer(kid, "identity", "bekratos-api");
        verifier.insert_key(kid.into(), &public_pem).await.unwrap();
        let token = signer
            .issue_access_token(Uuid::now_v7(), Uuid::now_v7())
            .unwrap();
        assert!(verifier.verify_access_token(&token).is_ok());
        tokens.push(token);
    }
    let cloned = verifier.clone();
    cloned.clear().await.unwrap();
    for token in tokens {
        assert!(matches!(
            verifier.verify_access_token(&token),
            Err(identity::AppError::Unauthorized)
        ));
        assert!(matches!(
            cloned.verify_access_token_async(&token).await,
            Err(identity::AppError::Unauthorized)
        ));
    }
    cloned.clear().await.unwrap();
    cloned.remove_key("already-absent").await.unwrap();
}

#[tokio::test]
async fn replacing_a_key_under_the_same_id_rejects_old_signatures() {
    let (old_signer, old_public) = make_signer("reused-kid", "identity", "bekratos-api");
    let (new_signer, new_public) = make_signer("reused-kid", "identity", "bekratos-api");
    let verifier = JwtVerifier::new("identity".into(), "bekratos-api".into());
    verifier
        .insert_key("reused-kid".into(), &old_public)
        .await
        .unwrap();
    let old_token = old_signer
        .issue_access_token(Uuid::now_v7(), Uuid::now_v7())
        .unwrap();
    assert!(verifier.verify_access_token(&old_token).is_ok());
    verifier
        .insert_key("reused-kid".into(), &new_public)
        .await
        .unwrap();
    assert!(matches!(
        verifier.verify_access_token(&old_token),
        Err(identity::AppError::Unauthorized)
    ));
    let new_token = new_signer
        .issue_access_token(Uuid::now_v7(), Uuid::now_v7())
        .unwrap();
    assert!(verifier.verify_access_token(&new_token).is_ok());
}

#[tokio::test]
async fn invalid_key_insert_does_not_discard_a_working_key() {
    let (signer, public_pem) = make_signer("working", "identity", "bekratos-api");
    let verifier = JwtVerifier::new("identity".into(), "bekratos-api".into());
    verifier
        .insert_key("working".into(), &public_pem)
        .await
        .unwrap();
    assert!(matches!(
        verifier.insert_key("working".into(), "invalid PEM").await,
        Err(identity::AppError::Internal)
    ));
    let token = signer
        .issue_access_token(Uuid::now_v7(), Uuid::now_v7())
        .unwrap();
    assert!(verifier.verify_access_token(&token).is_ok());
}

#[tokio::test]
async fn rejects_missing_key_ids_and_incomplete_or_wrongly_typed_claims() {
    let key = SigningKey::random(&mut OsRng);
    let private_pem = key.to_pkcs8_pem(LineEnding::LF).unwrap();
    let public_pem = public_pem_from_private_pem(&private_pem).unwrap();
    let encoding_key = EncodingKey::from_ec_pem(private_pem.as_bytes()).unwrap();
    let verifier = JwtVerifier::new("identity".into(), "bekratos-api".into());
    verifier
        .insert_key("claims-key".into(), &public_pem)
        .await
        .unwrap();
    let now = chrono::Utc::now().timestamp();
    let claims = serde_json::json!({
        "sub": Uuid::now_v7().to_string(), "sid": Uuid::now_v7().to_string(),
        "iss": "identity", "aud": "bekratos-api", "iat": now, "nbf": now, "exp": now + 900
    });
    let mut header = Header::new(Algorithm::ES256);
    let no_kid = encode(&header, &claims, &encoding_key).unwrap();
    assert!(matches!(
        verifier.verify_access_token(&no_kid),
        Err(identity::AppError::Unauthorized)
    ));
    header.kid = Some("claims-key".into());
    let valid_token = encode(&header, &claims, &encoding_key).unwrap();
    assert!(verifier.verify_access_token(&valid_token).is_ok());
    for field in ["sub", "sid", "iss", "aud", "iat", "nbf", "exp"] {
        for replacement in [None, Some(serde_json::json!({"invalid": true}))] {
            let mut invalid = claims.clone();
            if let Some(value) = replacement {
                invalid[field] = value;
            } else {
                invalid.as_object_mut().unwrap().remove(field);
            }
            let token = encode(&header, &invalid, &encoding_key).unwrap();
            assert!(
                matches!(
                    verifier.verify_access_token(&token),
                    Err(identity::AppError::Unauthorized)
                ),
                "accepted invalid {field}"
            );
        }
    }
    for malformed in ["", "not-a-token", "a.b.c", "a.b.c.d"] {
        assert!(matches!(
            verifier.verify_access_token(malformed),
            Err(identity::AppError::Unauthorized)
        ));
    }
}

#[tokio::test]
async fn rejects_modified_payloads_that_retain_the_original_header_and_signature() {
    use base64::engine::general_purpose::URL_SAFE_NO_PAD;
    use base64::Engine;
    use serde_json::{json, Value};

    let (signer, public_pem) = make_signer("payload-key", "identity", "bekratos-api");
    let verifier = JwtVerifier::new("identity".into(), "bekratos-api".into());
    verifier
        .insert_key("payload-key".into(), &public_pem)
        .await
        .unwrap();
    let user_id = Uuid::now_v7();
    let session_id = Uuid::now_v7();
    let token = signer.issue_access_token(user_id, session_id).unwrap();
    let parts: Vec<_> = token.split('.').collect();
    let claims: Value = serde_json::from_slice(&URL_SAFE_NO_PAD.decode(parts[1]).unwrap()).unwrap();
    assert!(verifier.verify_access_token(&token).is_ok());

    for (field, replacement) in [
        ("sub", json!(Uuid::now_v7().to_string())),
        ("sid", json!(Uuid::now_v7().to_string())),
        ("exp", json!(claims["exp"].as_i64().unwrap() + 3600)),
    ] {
        let mut modified = claims.clone();
        modified[field] = replacement;
        let payload = URL_SAFE_NO_PAD.encode(serde_json::to_vec(&modified).unwrap());
        let tampered = format!("{}.{payload}.{}", parts[0], parts[2]);
        assert!(
            matches!(
                verifier.verify_access_token(&tampered),
                Err(identity::AppError::Unauthorized)
            ),
            "accepted modified {field} with the original signature"
        );
        assert!(matches!(
            verifier.verify_access_token_async(&tampered).await,
            Err(identity::AppError::Unauthorized)
        ));
    }
    let verified = verifier.verify_access_token(&token).unwrap();
    assert_eq!(verified.sub, user_id.to_string());
    assert_eq!(verified.sid, session_id.to_string());
}
