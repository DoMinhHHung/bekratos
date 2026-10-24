use identity::infra::jwt::JwtSigner;
use identity::infra::refresh_token::{generate_opaque_token, hash_token};
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
    let pem = match std::fs::read_to_string("tests/fixtures/jwt-ec-private.pem") {
        Ok(pem) => pem,
        Err(error) if error.kind() == std::io::ErrorKind::NotFound => {
            eprintln!("skip jwt test: no tests/fixtures/jwt-ec-private.pem");
            return;
        }
        Err(error) => panic!("failed to read JWT test fixture: {error}"),
    };
    let signer = JwtSigner::from_pem(&pem, "es256-1".into(), "iss".into(), "aud".into(), 900)
        .expect("signer");
    let token = signer
        .issue_access_token(Uuid::now_v7(), Uuid::now_v7())
        .expect("issue");
    assert_eq!(token.split('.').count(), 3);
}
