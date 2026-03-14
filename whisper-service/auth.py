"""Registration and token management for whisper-service."""

import secrets
from fastapi import HTTPException
from fastapi.security import HTTPBearer, HTTPAuthorizationCredentials
from pydantic import BaseModel


class RegisterRequest(BaseModel):
    instance_id: str
    secret: str


class RegisterResponse(BaseModel):
    token: str
    instance_id: str


security = HTTPBearer()


class AuthManager:
    def __init__(self, shared_secret: str):
        self._shared_secret = shared_secret
        self._tokens: dict[str, str] = {}  # token → instance_id
        self._instance_tokens: dict[str, str] = {}  # instance_id → token

    def register(self, req: RegisterRequest) -> RegisterResponse:
        if not secrets.compare_digest(req.secret, self._shared_secret):
            raise HTTPException(status_code=401, detail="Invalid secret")

        # Revoke old token if instance already registered
        if req.instance_id in self._instance_tokens:
            old_token = self._instance_tokens[req.instance_id]
            self._tokens.pop(old_token, None)

        token = secrets.token_hex(32)
        self._tokens[token] = req.instance_id
        self._instance_tokens[req.instance_id] = token

        return RegisterResponse(token=token, instance_id=req.instance_id)

    def validate_token(self, credentials: HTTPAuthorizationCredentials) -> str:
        token = credentials.credentials
        instance_id = self._tokens.get(token)
        if instance_id is None:
            raise HTTPException(status_code=401, detail="Invalid token")
        return instance_id

    @property
    def registered_count(self) -> int:
        return len(self._instance_tokens)
