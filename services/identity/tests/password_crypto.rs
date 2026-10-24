use identity::infra::password::PasswordPolicy;

#[test]
fn hash_and_verify_roundtrip() {
    let policy = PasswordPolicy::new(19_456, 2, 1).unwrap();
    let hash = policy.hash("correct-horse-battery").unwrap();
    assert!(policy.verify("correct-horse-battery", &hash).unwrap());
    assert!(!policy.verify("wrong-password", &hash).unwrap());
}

#[test]
fn needs_rehash_detects_param_change() {
    let policy = PasswordPolicy::new(19_456, 2, 1).unwrap();
    let hash = policy.hash("correct-horse-battery").unwrap();
    assert!(!policy.needs_rehash(&hash));
    let stricter = PasswordPolicy::new(19_456, 3, 1).unwrap();
    assert!(stricter.needs_rehash(&hash));
}

#[test]
fn rejects_policy_parameters_below_the_security_minimum() {
    for (memory, iterations, parallelism) in [
        (19_455, 2, 1),
        (19_456, 1, 1),
        (19_456, 2, 0),
        (19_456, 2, 3_000),
    ] {
        assert!(matches!(
            PasswordPolicy::new(memory, iterations, parallelism),
            Err(identity::AppError::Config(_))
        ));
    }
}

#[test]
fn hashing_enforces_the_password_length_boundaries() {
    let policy = PasswordPolicy::new(19_456, 2, 1).unwrap();
    for length in [0, 7, 129] {
        assert!(matches!(
            policy.hash(&"a".repeat(length)),
            Err(identity::AppError::BadRequest(_))
        ));
    }
    for length in [8, 128] {
        let password = "a".repeat(length);
        let hash = policy.hash(&password).unwrap();
        assert!(policy.verify(&password, &hash).unwrap());
    }
}

#[test]
fn hashing_counts_utf8_bytes_at_the_length_limit() {
    let policy = PasswordPolicy::new(19_456, 2, 1).unwrap();
    let password = "é".repeat(64);
    let hash = policy.hash(&password).unwrap();
    assert!(policy.verify(&password, &hash).unwrap());
    assert!(matches!(
        policy.hash(&"é".repeat(65)),
        Err(identity::AppError::BadRequest(_))
    ));
}

#[test]
fn hashes_use_argon2id_and_independent_salts() {
    let policy = PasswordPolicy::new(19_456, 2, 1).unwrap();
    let first = policy.hash("same-password").unwrap();
    let second = policy.hash("same-password").unwrap();
    let first_phc = password_hash::PasswordHash::new(&first).unwrap();
    let second_phc = password_hash::PasswordHash::new(&second).unwrap();
    assert_eq!(first_phc.algorithm.as_str(), "argon2id");
    assert_eq!(first_phc.version, Some(19));
    assert_ne!(first_phc.salt, second_phc.salt);
    assert!(policy.verify("same-password", &first).unwrap());
    assert!(policy.verify("same-password", &second).unwrap());
}

#[test]
fn malformed_or_unsupported_hashes_fail_closed_and_need_rehash() {
    let policy = PasswordPolicy::new(19_456, 2, 1).unwrap();
    for hash in [
        "",
        "not a PHC string",
        "$argon2id$v=19$m=19456,t=2,p=1$invalid!$invalid!",
        "$unknown$v=19$m=19456,t=2,p=1$c29tZXNhbHQ$AAAAAAAAAAAAAAAAAAAAAA",
    ] {
        assert!(!policy.verify("some-password", hash).unwrap());
        assert!(policy.needs_rehash(hash));
    }
}

#[test]
fn needs_rehash_detects_each_parameter_and_algorithm_change() {
    let policy = PasswordPolicy::new(19_456, 2, 1).unwrap();
    let hash = policy.hash("correct-horse-battery").unwrap();
    for (memory, iterations, parallelism) in [(19_457, 2, 1), (19_456, 3, 1), (19_456, 2, 2)] {
        let changed = PasswordPolicy::new(memory, iterations, parallelism).unwrap();
        assert!(changed.needs_rehash(&hash));
        assert!(changed.verify("correct-horse-battery", &hash).unwrap());
    }
    assert!(policy.needs_rehash(&hash.replacen("argon2id", "argon2i", 1)));
    assert!(policy.needs_rehash(&hash.replacen("m=19456,", "", 1)));
    assert!(policy.needs_rehash(&hash.replacen("t=2,", "", 1)));
    assert!(policy.needs_rehash(&hash.replacen(",p=1", "", 1)));
}
