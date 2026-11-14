"""Service errors that map onto the shared error envelope.

The Go platform decides retry behaviour from the status and code:
  - 503 AI_PROVIDER_UNAVAILABLE: no usable credentials (permanent until an operator acts)
  - 503 AI_PROVIDER_TRANSIENT:   rate limit / overload / network (retryable)
  - 502 AI_PROVIDER_REJECTED:    provider rejected the request (permanent)
  - 502 AI_REFUSED:              provider declined for safety reasons (permanent)
  - 502 AI_OUTPUT_INVALID:       structured output failed validation (retryable)
  - 422 UNREADABLE_DOCUMENT / DOCUMENT_TOO_LONG: input problem (permanent)
"""

from __future__ import annotations


class ServiceError(Exception):
    def __init__(self, code: str, message: str, status: int) -> None:
        super().__init__(message)
        self.code = code
        self.message = message
        self.status = status


class ProviderUnavailable(ServiceError):
    def __init__(self, message: str = "No AI provider is configured.") -> None:
        super().__init__("AI_PROVIDER_UNAVAILABLE", message, 503)


class ProviderTransient(ServiceError):
    def __init__(self, message: str = "The AI provider is temporarily unavailable.") -> None:
        super().__init__("AI_PROVIDER_TRANSIENT", message, 503)


class ProviderRejected(ServiceError):
    def __init__(self, message: str = "The AI provider rejected the request.") -> None:
        super().__init__("AI_PROVIDER_REJECTED", message, 502)


class ProviderRefused(ServiceError):
    def __init__(self, message: str = "The AI provider declined to process this content.") -> None:
        super().__init__("AI_REFUSED", message, 502)


class OutputInvalid(ServiceError):
    def __init__(self, message: str = "The AI output did not match the expected structure.") -> None:
        super().__init__("AI_OUTPUT_INVALID", message, 502)


class UnreadableDocument(ServiceError):
    def __init__(self, message: str) -> None:
        super().__init__("UNREADABLE_DOCUMENT", message, 422)


class DocumentTooLong(ServiceError):
    def __init__(self, message: str) -> None:
        super().__init__("DOCUMENT_TOO_LONG", message, 422)
