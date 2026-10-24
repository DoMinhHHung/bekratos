use identity::infra::jwt::JwtSigner;
use identity::infra::refresh_token::{generate_opaque_token, hash_token};
use p256::ecdsa::SigningKey;
use p256::pkcs8::{EncodePrivateKey, LineEnding};
use rand::rngs::OsRng;
use uuid::Uuid;

#[test]
fn opaque_token_hash_roundtrip_length() {
    let raw = generate_opaque_token();
    assert_eq!(raw.len(), 64);
    let h = hash_token(&raw);
    assert_eq!(h.len(), 64);
    assert_ne!(raw, h);
}

#[test]
fn es256_sign_produces_three_segments() {
    let pem = SigningKey::random(&mut OsRng)
        .to_pkcs8_pem(LineEnding::LF)
        .expect("generate ephemeral test key");
    let signer = JwtSigner::from_pem(&pem, "es256-1".into(), "iss".into(), "aud".into(), 900)
        .expect("signer");
    let token = signer
        .issue_access_token(Uuid::now_v7(), Uuid::now_v7())
        .expect("issue");
    assert_eq!(token.split('.').count(), 3);
}

#[test]
fn opaque_tokens_are_unique_32_byte_lowercase_hex_values() {
    let tokens: std::collections::HashSet<_> = (0..64).map(|_| generate_opaque_token()).collect();
    assert_eq!(tokens.len(), 64);
    for token in tokens {
        assert_eq!(hex::decode(&token).unwrap().len(), 32);
        assert_eq!(token, token.to_ascii_lowercase());
    }
}

#[test]
fn token_hash_matches_standard_sha256_vectors() {
    for (input, expected) in [
        (
            "",
            "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855",
        ),
        (
            "abc",
            "ba7816bf8f01cfea414140de5dae2223b00361a396177a9cb410ff61f20015ad",
        ),
    ] {
        assert_eq!(hash_token(input), expected);
    }
}

#[test]
fn token_hash_preserves_case_and_whitespace_distinctions() {
    let original = hash_token("opaque-token");
    assert_eq!(original, hash_token("opaque-token"));
    for different in [
        "Opaque-token",
        " opaque-token",
        "opaque-token ",
        "opaque-token\0",
    ] {
        assert_ne!(original, hash_token(different));
    }
}
