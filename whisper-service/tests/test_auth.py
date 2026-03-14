"""Tests for auth module."""

import pytest
from fastapi import HTTPException
from unittest.mock import MagicMock

from auth import AuthManager, RegisterRequest


@pytest.fixture
def manager():
    return AuthManager(shared_secret="test-secret")


def _creds(token: str):
    mock = MagicMock()
    mock.credentials = token
    return mock


def test_register_valid_secret(manager):
    resp = manager.register(RegisterRequest(instance_id="alf-01", secret="test-secret"))
    assert len(resp.token) == 64
    assert resp.instance_id == "alf-01"


def test_register_invalid_secret(manager):
    with pytest.raises(HTTPException) as exc:
        manager.register(RegisterRequest(instance_id="alf-01", secret="wrong"))
    assert exc.value.status_code == 401


def test_register_replaces_old_token(manager):
    resp1 = manager.register(RegisterRequest(instance_id="alf-01", secret="test-secret"))
    resp2 = manager.register(RegisterRequest(instance_id="alf-01", secret="test-secret"))

    assert resp1.token != resp2.token

    # Old token is invalid
    with pytest.raises(HTTPException):
        manager.validate_token(_creds(resp1.token))

    # New token works
    assert manager.validate_token(_creds(resp2.token)) == "alf-01"


def test_validate_valid_token(manager):
    resp = manager.register(RegisterRequest(instance_id="alf-01", secret="test-secret"))
    assert manager.validate_token(_creds(resp.token)) == "alf-01"


def test_validate_invalid_token(manager):
    with pytest.raises(HTTPException) as exc:
        manager.validate_token(_creds("invalid-token"))
    assert exc.value.status_code == 401


def test_registered_count(manager):
    assert manager.registered_count == 0
    manager.register(RegisterRequest(instance_id="alf-01", secret="test-secret"))
    assert manager.registered_count == 1
    manager.register(RegisterRequest(instance_id="alf-02", secret="test-secret"))
    assert manager.registered_count == 2
    # Re-register same instance doesn't increase count
    manager.register(RegisterRequest(instance_id="alf-01", secret="test-secret"))
    assert manager.registered_count == 2
