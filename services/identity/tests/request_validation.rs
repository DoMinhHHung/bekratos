use identity::modules::auth::dto::{
    ChangePasswordRequest, ForgotPasswordRequest, LoginRequest, RefreshRequest, RegisterRequest,
    ResendVerificationRequest, ResetPasswordRequest, RestoreConfirmRequest, RestoreRequestBody,
    VerifyEmailRequest,
};
use identity::modules::user::dto::UpdateProfileRequest;
use serde::de::DeserializeOwned;
use serde_json::{json, Value};
use validator::Validate;

fn assert_length_boundaries<T: DeserializeOwned + Validate>(
    baseline: Value,
    field: &str,
    min: usize,
    max: usize,
) {
    for length in [0, min - 1, min, max, max + 1] {
        let mut body = baseline.clone();
        body[field] = json!("a".repeat(length));
        let request: T = serde_json::from_value(body).unwrap();
        let result = request.validate();
        if (min..=max).contains(&length) {
            assert!(
                result.is_ok(),
                "{field} rejected {length} characters: {result:?}"
            );
        } else {
            let errors = result.expect_err("out-of-range field must be rejected");
            assert!(
                errors.field_errors().contains_key(field),
                "missing validation error for {field}"
            );
        }
    }
}

fn registration() -> Value {
    json!({
        "email": "person@example.test", "username": "person_1", "password": "password-for-tests",
        "accept_policy": true
    })
}

#[test]
fn registration_enforces_username_and_password_length_boundaries() {
    assert_length_boundaries::<RegisterRequest>(registration(), "username", 3, 30);
    assert_length_boundaries::<RegisterRequest>(registration(), "password", 8, 128);
}

#[test]
fn login_enforces_identifier_and_password_length_boundaries() {
    let body = json!({"login": "person_1", "password": "password-for-tests"});
    assert_length_boundaries::<LoginRequest>(body.clone(), "login", 3, 254);
    assert_length_boundaries::<LoginRequest>(body, "password", 8, 128);
}

#[test]
fn login_accepts_email_and_username_identifiers_without_a_device_name() {
    for login in ["person_1", "person@example.test"] {
        let request: LoginRequest = serde_json::from_value(json!({
            "login": login, "password": "password-for-tests"
        }))
        .unwrap();
        assert!(request.validate().is_ok());
        assert!(request.device_name.is_none());
    }
}

#[test]
fn verification_reset_and_restore_tokens_share_the_documented_length_limits() {
    assert_length_boundaries::<VerifyEmailRequest>(json!({}), "token", 10, 512);
    assert_length_boundaries::<RestoreConfirmRequest>(json!({}), "token", 10, 512);
    assert_length_boundaries::<ResetPasswordRequest>(
        json!({"new_password": "password-for-tests"}),
        "token",
        10,
        512,
    );
}

#[test]
fn refresh_token_length_boundaries_are_enforced() {
    assert_length_boundaries::<RefreshRequest>(json!({}), "refresh_token", 32, 128);
}

#[test]
fn reset_and_change_password_validate_each_password_independently() {
    assert_length_boundaries::<ResetPasswordRequest>(
        json!({"token": "valid-token-value"}),
        "new_password",
        8,
        128,
    );
    assert_length_boundaries::<ChangePasswordRequest>(
        json!({"new_password": "password-for-tests"}),
        "current_password",
        8,
        128,
    );
    assert_length_boundaries::<ChangePasswordRequest>(
        json!({"current_password": "password-for-tests"}),
        "new_password",
        8,
        128,
    );
}

fn assert_email_validation<T: DeserializeOwned + Validate>(mut baseline: Value) {
    for email in [
        "person@example.test",
        "first.last+tag@example.test",
        "",
        "person",
        "@example.test",
        "person@",
        "person name@example.test",
    ] {
        baseline["email"] = json!(email);
        let request: T = serde_json::from_value(baseline.clone()).unwrap();
        let result = request.validate();
        if email == "person@example.test" || email == "first.last+tag@example.test" {
            assert!(result.is_ok());
        } else {
            assert!(result.unwrap_err().field_errors().contains_key("email"));
        }
    }
}

#[test]
fn every_email_request_rejects_invalid_addresses_and_accepts_tagged_addresses() {
    assert_email_validation::<RegisterRequest>(registration());
    assert_email_validation::<ResendVerificationRequest>(json!({}));
    assert_email_validation::<ForgotPasswordRequest>(json!({}));
    assert_email_validation::<RestoreRequestBody>(json!({}));
}

#[test]
fn registration_reports_all_invalid_fields_together() {
    let request: RegisterRequest = serde_json::from_value(json!({
        "email": "invalid", "username": "a", "password": "short", "accept_policy": true
    }))
    .unwrap();
    let errors = request.validate().unwrap_err();
    let fields = errors.field_errors();
    assert_eq!(fields.len(), 3);
    for field in ["email", "username", "password"] {
        assert!(fields.contains_key(field));
    }
}

#[test]
fn registration_requires_all_mandatory_fields_and_their_json_types() {
    for field in ["email", "username", "password", "accept_policy"] {
        let mut body = registration();
        body.as_object_mut().unwrap().remove(field);
        assert!(
            serde_json::from_value::<RegisterRequest>(body).is_err(),
            "accepted missing {field}"
        );

        let mut body = registration();
        body[field] = json!(42);
        assert!(
            serde_json::from_value::<RegisterRequest>(body).is_err(),
            "accepted wrong type for {field}"
        );
    }
    let request: RegisterRequest = serde_json::from_value(registration()).unwrap();
    assert!(request.locale.is_none());
    assert!(request.timezone.is_none());
    assert!(request.full_name.is_none());
}

#[test]
fn profile_patch_accepts_omitted_and_null_optional_fields() {
    for body in [
        json!({}),
        json!({
            "full_name": null, "display_name": null, "avatar_url": null, "locale": null, "timezone": null
        }),
    ] {
        let request: UpdateProfileRequest = serde_json::from_value(body).unwrap();
        assert!(request.validate().is_ok());
        assert!(request.full_name.is_none());
        assert!(request.display_name.is_none());
        assert!(request.avatar_url.is_none());
        assert!(request.locale.is_none());
        assert!(request.timezone.is_none());
    }
}

#[test]
fn profile_patch_enforces_each_field_limit_and_accepts_the_boundary() {
    for (field, max) in [
        ("full_name", 120),
        ("display_name", 60),
        ("avatar_url", 512),
        ("locale", 16),
        ("timezone", 64),
    ] {
        for length in [0, max, max + 1] {
            let mut body = json!({});
            body[field] = json!("a".repeat(length));
            let request: UpdateProfileRequest = serde_json::from_value(body).unwrap();
            let result = request.validate();
            if length <= max {
                assert!(result.is_ok(), "{field} rejected {length} characters");
            } else {
                assert!(result.unwrap_err().field_errors().contains_key(field));
            }
        }
    }
}

#[test]
fn profile_name_limits_count_unicode_characters() {
    let valid: UpdateProfileRequest = serde_json::from_value(json!({
        "full_name": "é".repeat(120), "display_name": "界".repeat(60)
    }))
    .unwrap();
    assert!(valid.validate().is_ok());
    let invalid: UpdateProfileRequest = serde_json::from_value(json!({
        "full_name": "é".repeat(121), "display_name": "界".repeat(61)
    }))
    .unwrap();
    let errors = invalid.validate().unwrap_err();
    let fields = errors.field_errors();
    assert_eq!(fields.len(), 2);
    assert!(fields.contains_key("full_name"));
    assert!(fields.contains_key("display_name"));
}

#[test]
fn profile_patch_reports_all_overlong_fields_in_one_validation_result() {
    let request: UpdateProfileRequest = serde_json::from_value(json!({
        "full_name": "a".repeat(121),
        "display_name": "a".repeat(61),
        "avatar_url": "a".repeat(513),
        "locale": "a".repeat(17),
        "timezone": "a".repeat(65)
    }))
    .unwrap();
    let errors = request.validate().unwrap_err();
    let fields = errors.field_errors();
    assert_eq!(fields.len(), 5);
    for field in [
        "full_name",
        "display_name",
        "avatar_url",
        "locale",
        "timezone",
    ] {
        assert!(fields.contains_key(field), "missing error for {field}");
    }
}

#[test]
fn login_identifier_length_limits_count_unicode_characters() {
    for (length, valid) in [(2, false), (3, true), (254, true), (255, false)] {
        let request: LoginRequest = serde_json::from_value(json!({
            "login": "界".repeat(length), "password": "password-for-tests"
        }))
        .unwrap();
        let result = request.validate();
        if valid {
            assert!(result.is_ok(), "rejected {length} Unicode characters");
        } else {
            assert!(result.unwrap_err().field_errors().contains_key("login"));
        }
    }
}
