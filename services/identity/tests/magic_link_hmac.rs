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

fn signer_with_keys(active_kid: &str) -> MagicLinkSigner {
    MagicLinkSigner::new(
        vec![
            identity::infra::tokens::HmacKey {
                kid: "old".into(),
                secret: vec![11; 32],
            },
            identity::infra::tokens::HmacKey {
                kid: "new".into(),
                secret: vec![22; 32],
            },
        ],
        active_kid.into(),
    )
    .unwrap()
}

#[test]
fn issued_token_matches_an_independent_hmac_sha256_vector() {
    let signer = signer_with_keys("old");
    let id = Uuid::from_u128(1);
    // Python's hmac.digest with key [11; 32] over the domain, purpose, and raw UUID bytes.
    let expected = "old.00000000-0000-0000-0000-000000000001.19ef760d1ea579f470bc2cbecc36a28f3059de1d43d6cdd5cee33fd92d070c50";
    assert_eq!(signer.issue("email_verify", id).unwrap(), expected);
    assert_eq!(signer.verify("email_verify", expected).unwrap(), id);
}

#[test]
fn parses_multiple_keys_with_whitespace_and_empty_separators() {
    let raw = format!(" , old : {} ,, new:{} , ", "AB".repeat(32), "cd".repeat(32));
    let keys = parse_hmac_keys(&raw).unwrap();
    assert_eq!(keys.len(), 2);
    assert_eq!(keys[0].kid, "old");
    assert_eq!(keys[0].secret, vec![0xab; 32]);
    assert_eq!(keys[1].kid, "new");
    assert_eq!(keys[1].secret, vec![0xcd; 32]);
}

#[test]
fn rejects_empty_keyrings_missing_separators_and_invalid_hex() {
    for raw in [
        "",
        " , , ",
        "key-without-secret",
        "v1:0",
        "v1:gg",
        "v1:00:11",
    ] {
        assert!(matches!(
            parse_hmac_keys(raw),
            Err(identity::AppError::Config(_))
        ));
    }
}

#[test]
fn construction_requires_an_active_key_and_strong_secrets_for_every_key() {
    use identity::infra::tokens::HmacKey;
    for (keys, active) in [
        (vec![], "active"),
        (
            vec![HmacKey {
                kid: "other".into(),
                secret: vec![0; 32],
            }],
            "active",
        ),
        (
            vec![HmacKey {
                kid: "active".into(),
                secret: vec![0; 31],
            }],
            "active",
        ),
        (
            vec![
                HmacKey {
                    kid: "active".into(),
                    secret: vec![0; 32],
                },
                HmacKey {
                    kid: "retired".into(),
                    secret: vec![1; 31],
                },
            ],
            "active",
        ),
    ] {
        assert!(matches!(
            MagicLinkSigner::new(keys, active.into()),
            Err(identity::AppError::Config(_))
        ));
    }
}

#[test]
fn issues_with_the_active_key_and_verifies_both_keys_during_rotation() {
    let id = Uuid::now_v7();
    let old_signer = signer_with_keys("old");
    let new_signer = signer_with_keys("new");
    let old_token = old_signer.issue("email_verify", id).unwrap();
    let new_token = new_signer.issue("email_verify", id).unwrap();
    assert!(old_token.starts_with("old."));
    assert!(new_token.starts_with("new."));
    assert_ne!(old_token, new_token);
    assert_eq!(new_signer.verify("email_verify", &old_token).unwrap(), id);
    assert_eq!(old_signer.verify("email_verify", &new_token).unwrap(), id);
}

#[test]
fn removing_a_retired_key_invalidates_its_links() {
    let old_token = signer_with_keys("old")
        .issue("account_restore", Uuid::now_v7())
        .unwrap();
    let new_only = MagicLinkSigner::new(
        vec![identity::infra::tokens::HmacKey {
            kid: "new".into(),
            secret: vec![22; 32],
        }],
        "new".into(),
    )
    .unwrap();
    assert!(matches!(
        new_only.verify("account_restore", &old_token),
        Err(identity::AppError::BadRequest(_))
    ));
}

#[test]
fn rejects_changed_mac_id_key_and_secret_even_when_the_format_is_valid() {
    let signer = signer_with_keys("old");
    let id = Uuid::now_v7();
    let token = signer.issue("email_verify", id).unwrap();
    let parts: Vec<_> = token.split('.').collect();
    let mut mac = parts[2].to_string();
    mac.replace_range(..1, if mac.starts_with('0') { "1" } else { "0" });
    for tampered in [
        format!("old.{id}.{mac}"),
        format!("old.{}.{}", Uuid::now_v7(), parts[2]),
        format!("new.{id}.{}", parts[2]),
        format!("unknown.{id}.{}", parts[2]),
    ] {
        assert!(matches!(
            signer.verify("email_verify", &tampered),
            Err(identity::AppError::BadRequest(_))
        ));
    }
    let wrong_secret = MagicLinkSigner::new(
        vec![identity::infra::tokens::HmacKey {
            kid: "old".into(),
            secret: vec![33; 32],
        }],
        "old".into(),
    )
    .unwrap();
    assert!(matches!(
        wrong_secret.verify("email_verify", &token),
        Err(identity::AppError::BadRequest(_))
    ));
}

#[test]
fn rejects_malformed_segments_ids_and_mac_encodings() {
    let signer = signer_with_keys("old");
    let id = Uuid::now_v7();
    for token in [
        "".into(),
        "old".into(),
        format!("old.{id}"),
        format!("old.{id}.{}.extra", "0".repeat(64)),
        format!("old.not-a-uuid.{}", "0".repeat(64)),
        format!("old..{}", "0".repeat(64)),
        format!("old.{id}."),
        format!("old.{id}.{}", "0".repeat(63)),
        format!("old.{id}.{}", "0".repeat(65)),
        format!("old.{id}.{}", "g".repeat(64)),
        format!("old.{id}.{}", "é".repeat(32)),
    ] {
        assert!(matches!(
            signer.verify("email_verify", &token),
            Err(identity::AppError::BadRequest(_))
        ));
    }
}

#[test]
fn every_magic_link_purpose_is_cryptographically_separated() {
    let signer = signer_with_keys("old");
    let id = Uuid::now_v7();
    let purposes = ["email_verify", "password_reset", "account_restore"];
    for purpose in purposes {
        let token = signer.issue(purpose, id).unwrap();
        assert_eq!(token, signer.issue(purpose, id).unwrap());
        assert_eq!(signer.verify(purpose, &token).unwrap(), id);
        for other in purposes.into_iter().filter(|other| *other != purpose) {
            assert!(matches!(
                signer.verify(other, &token),
                Err(identity::AppError::BadRequest(_))
            ));
        }
    }
}
