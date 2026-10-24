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
