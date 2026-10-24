use chacha20poly1305::aead::{Aead, KeyInit};
use chacha20poly1305::{Key, XChaCha20Poly1305, XNonce};
use identity::infra::key_crypto::KeyEncryptor;
use identity::AppError;
use sha2::{Digest, Sha256};

fn test_kek() -> String {
    "local-encryption-test-key-".repeat(2)
}

#[test]
fn encrypts_and_decrypts_empty_text_and_binary_payloads() {
    let encryptor = KeyEncryptor::new(&test_kek()).unwrap();
    for plaintext in [b"".as_slice(), b"signing-key-material", &[0, 255, 128, 1]] {
        let encrypted = encryptor.encrypt(plaintext).unwrap();
        assert_eq!(encrypted.len(), plaintext.len() + 24 + 16);
        assert_eq!(encryptor.decrypt(&encrypted).unwrap(), plaintext);
        assert_eq!(
            encryptor.decrypt_and_was_legacy(&encrypted).unwrap(),
            (plaintext.to_vec(), false)
        );
    }
}

#[test]
fn encrypting_the_same_plaintext_uses_distinct_nonces() {
    let encryptor = KeyEncryptor::new(&test_kek()).unwrap();
    let first = encryptor.encrypt(b"same plaintext").unwrap();
    let second = encryptor.encrypt(b"same plaintext").unwrap();
    assert_ne!(&first[..24], &second[..24]);
    assert_eq!(encryptor.decrypt(&first).unwrap(), b"same plaintext");
    assert_eq!(encryptor.decrypt(&second).unwrap(), b"same plaintext");
}

#[test]
fn decrypt_rejects_every_truncation_of_a_ciphertext() {
    let encryptor = KeyEncryptor::new(&test_kek()).unwrap();
    let encrypted = encryptor.encrypt(b"private key").unwrap();
    for length in 0..encrypted.len() {
        assert!(
            matches!(
                encryptor.decrypt(&encrypted[..length]),
                Err(AppError::Internal)
            ),
            "accepted ciphertext truncated to {length} bytes"
        );
    }
}

#[test]
fn decrypt_authenticates_nonce_ciphertext_and_tag() {
    let encryptor = KeyEncryptor::new(&test_kek()).unwrap();
    let encrypted = encryptor.encrypt(b"private key").unwrap();
    for index in [0, 23, 24, encrypted.len() - 1] {
        let mut tampered = encrypted.clone();
        tampered[index] ^= 1;
        assert!(matches!(
            encryptor.decrypt(&tampered),
            Err(AppError::Internal)
        ));
    }
}

#[test]
fn current_derivation_uses_kek_bytes_beyond_the_first_32() {
    let prefix = "k".repeat(32);
    let first = KeyEncryptor::new(&format!("{prefix}-first")).unwrap();
    let second = KeyEncryptor::new(&format!("{prefix}-second")).unwrap();
    let encrypted = first.encrypt(b"private key").unwrap();
    assert!(matches!(
        second.decrypt(&encrypted),
        Err(AppError::Internal)
    ));
}

// Produce the historical wire format independently of KeyEncryptor::encrypt.
fn legacy_ciphertext(kek: &str, plaintext: &[u8]) -> Vec<u8> {
    let key_bytes: [u8; 32] = if kek.len() < 32 {
        Sha256::digest(kek.as_bytes()).into()
    } else {
        kek.as_bytes()[..32].try_into().unwrap()
    };
    let cipher = XChaCha20Poly1305::new(Key::from_slice(&key_bytes));
    let nonce_bytes = [7u8; 24];
    let mut result = nonce_bytes.to_vec();
    result.extend(
        cipher
            .encrypt(XNonce::from_slice(&nonce_bytes), plaintext)
            .unwrap(),
    );
    result
}

#[test]
fn decrypts_legacy_keys_and_reencrypts_them_in_the_current_format() {
    for kek in [
        "short-legacy-key".to_string(),
        "l".repeat(32),
        "l".repeat(48),
    ] {
        let encryptor = KeyEncryptor::new(&kek).unwrap();
        let legacy = legacy_ciphertext(&kek, b"legacy private key");
        let (plaintext, was_legacy) = encryptor.decrypt_and_was_legacy(&legacy).unwrap();
        assert!(was_legacy);
        assert_eq!(plaintext, b"legacy private key");
        assert_eq!(encryptor.decrypt(&legacy).unwrap(), plaintext);

        let migrated = encryptor.encrypt(&plaintext).unwrap();
        assert_eq!(
            encryptor.decrypt_and_was_legacy(&migrated).unwrap(),
            (plaintext, false)
        );
    }
}

#[test]
fn legacy_fallback_rejects_tampered_ciphertext() {
    let kek = test_kek();
    let encryptor = KeyEncryptor::new(&kek).unwrap();
    let mut encrypted = legacy_ciphertext(&kek, b"legacy private key");
    *encrypted.last_mut().unwrap() ^= 1;
    assert!(matches!(
        encryptor.decrypt(&encrypted),
        Err(AppError::Internal)
    ));
}
