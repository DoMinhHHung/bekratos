use identity::infra::tokens::{parse_hmac_keys, MagicLinkSigner};
use uuid::Uuid;

#[test]
fn issue_and_verify() {
    let keys =
        parse_hmac_keys("v1:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
            .unwrap();
    let signer = MagicLinkSigner::new(keys, "v1".into()).unwrap();
    let id = Uuid::now_v7();
    let token = signer.issue("email_verify", id).unwrap();
    let got = signer.verify("email_verify", &token).unwrap();
    assert_eq!(got, id);
}

#[test]
fn reject_tampered_mac() {
    let keys =
        parse_hmac_keys("v1:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
            .unwrap();
    let signer = MagicLinkSigner::new(keys, "v1".into()).unwrap();
    let id = Uuid::now_v7();
    let mut token = signer.issue("email_verify", id).unwrap();
    token.push('0');
    assert!(signer.verify("email_verify", &token).is_err());
}

#[test]
fn reject_wrong_purpose() {
    let keys =
        parse_hmac_keys("v1:0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef")
            .unwrap();
    let signer = MagicLinkSigner::new(keys, "v1".into()).unwrap();
    let id = Uuid::now_v7();
    let token = signer.issue("email_verify", id).unwrap();
    assert!(signer.verify("password_reset", &token).is_err());
}
