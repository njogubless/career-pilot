import pytest
from fastapi.testclient import TestClient
from pydantic import ValidationError

from app.core.config import AppEnv, ProviderName, Settings
from app.main import create_app

TOKEN = "t" * 32


def client(**overrides) -> TestClient:
    settings = Settings(app_env=AppEnv.TEST, ai_service_token=TOKEN, **overrides)
    return TestClient(create_app(settings))


def test_healthz_is_public():
    r = client().get("/healthz")
    assert r.status_code == 200
    assert r.json()["status"] == "ok"


def test_internal_requires_token():
    c = client()
    assert c.get("/internal/v1/info").status_code == 401
    bad = c.get("/internal/v1/info", headers={"Authorization": "Bearer wrong"})
    assert bad.status_code == 401
    assert bad.json()["error"]["code"] == "UNAUTHENTICATED"


def test_info_reports_no_provider_honestly():
    r = client().get("/internal/v1/info", headers={"Authorization": f"Bearer {TOKEN}"})
    assert r.status_code == 200
    body = r.json()
    assert body["llm_provider"] == "none"
    assert body["llm_configured"] is False


def test_auto_provider_prefers_anthropic():
    s = Settings(app_env=AppEnv.TEST, anthropic_api_key="k1", openai_api_key="k2")
    assert s.resolved_provider is ProviderName.ANTHROPIC
    # OpenAI is not implemented yet: its key alone does not enable AI features.
    assert Settings(app_env=AppEnv.TEST, openai_api_key="k2").resolved_provider is ProviderName.NONE
    with pytest.raises(ValidationError):
        Settings(app_env=AppEnv.TEST, ai_provider=ProviderName.OPENAI, openai_api_key="k2")


def test_explicit_provider_requires_key():
    with pytest.raises(ValidationError):
        Settings(app_env=AppEnv.TEST, ai_provider=ProviderName.ANTHROPIC)


def test_production_requires_token_and_forbids_fake():
    with pytest.raises(ValidationError):
        Settings(app_env=AppEnv.PRODUCTION)
    with pytest.raises(ValidationError):
        Settings(app_env=AppEnv.PRODUCTION, ai_service_token=TOKEN, ai_provider=ProviderName.FAKE)
    assert Settings(app_env=AppEnv.PRODUCTION, ai_service_token=TOKEN).llm_configured is False


def test_production_hides_docs():
    s = Settings(app_env=AppEnv.PRODUCTION, ai_service_token=TOKEN)
    c = TestClient(create_app(s))
    assert c.get("/docs").status_code == 404
    assert c.get("/openapi.json").status_code == 404
