"""Service-to-service authentication (ADR-006)."""

from __future__ import annotations

import hmac
from typing import Annotated

from fastapi import Depends, Header, HTTPException, status

from app.core.config import AppEnv, Settings, get_settings


def require_service_token(
    settings: Annotated[Settings, Depends(get_settings)],
    authorization: Annotated[str | None, Header()] = None,
) -> None:
    """Reject requests that lack the shared service token.

    In development/test with no token configured, internal endpoints remain
    open so the service can run standalone; production always requires it
    (enforced at startup by Settings validation).
    """
    expected = settings.ai_service_token.get_secret_value()
    if not expected and settings.app_env is not AppEnv.PRODUCTION:
        return
    scheme, _, token = (authorization or "").partition(" ")
    if scheme.lower() != "bearer" or not hmac.compare_digest(token.encode(), expected.encode()):
        raise HTTPException(
            status_code=status.HTTP_401_UNAUTHORIZED,
            detail={"code": "UNAUTHENTICATED", "message": "Invalid service token."},
        )
