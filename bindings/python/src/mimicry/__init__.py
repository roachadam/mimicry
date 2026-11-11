"""mimicry: HTTP requests the way Chrome, Firefox and Safari send them.

The Python SDK of github.com/roachadam/mimicry (decision 0069): each process
starts its own ``mimicry-core``, the Go library as a child, and speaks to it
over a socketpair (pipes on Windows). Go's names in Python casing:

    import mimicry
    from mimicry import Config, Kind, Request, profiles

    client = mimicry.new_client(Config(profile=profiles.chrome_macos()))

    # A plain request is a page load, as if typed into the address bar.
    page = client.do(Request("GET", "https://www.shop.example/p/42"))

    # A fetch() from that page: Origin, Referer, sec-fetch-* and cookies follow.
    api = Request("GET", "https://www.shop.example/api/stock/42")
    resp = client.do(mimicry.as_kind(api, Kind.FETCH))

This module is the sync API; :mod:`mimicry.aio` is the async one, with the
same names.
"""

from __future__ import annotations

from . import profiles
from ._errors import (
    BlockedError,
    CanceledError,
    CloseError,
    Code,
    CoreExited,
    CORSError,
    DeadlineError,
    InternalError,
    InvalidError,
    MimicryError,
    NetError,
    NotMeasuredError,
    PinError,
    PlacementError,
    ProtocolError,
    ProxyError,
    ValidationError,
    VerifyError,
)
from ._sync import (
    AltSvcCache,
    Client,
    ClientHintStore,
    Core,
    Jar,
    Profile,
    Response,
    TicketCache,
    Transport,
    WebSocket,
    fetch_pins,
    list_profiles,
    machines,
    new_alt_svc_cache,
    new_client,
    new_client_hint_store,
    new_jar,
    new_ticket_cache,
    new_transport,
    parse_profile,
    profile_spec,
)
from ._types import (
    CertPin,
    CertRole,
    ClientCertificate,
    Config,
    ConnectionState,
    Cookie,
    CookieRequest,
    Credentials,
    CrossOrigin,
    Headers,
    HintDelegation,
    Kind,
    Lines,
    MessageType,
    Options,
    Override,
    Pin,
    Place,
    Plan,
    Priority,
    ProfileInfo,
    Request,
    ResponseHead,
    SameSite,
    SetCookieEvent,
    WebSocketOptions,
    as_kind,
    exact,
    with_options,
)
from ._version import __version__

__all__ = [
    "AltSvcCache",
    "BlockedError",
    "CORSError",
    "CanceledError",
    "CertPin",
    "CertRole",
    "Client",
    "ClientCertificate",
    "ClientHintStore",
    "CloseError",
    "Code",
    "Config",
    "ConnectionState",
    "Cookie",
    "CookieRequest",
    "Core",
    "CoreExited",
    "Credentials",
    "CrossOrigin",
    "DeadlineError",
    "Headers",
    "HintDelegation",
    "InternalError",
    "InvalidError",
    "Jar",
    "Kind",
    "Lines",
    "MessageType",
    "MimicryError",
    "NetError",
    "NotMeasuredError",
    "Options",
    "Override",
    "Pin",
    "PinError",
    "Place",
    "PlacementError",
    "Plan",
    "Priority",
    "Profile",
    "ProfileInfo",
    "ProtocolError",
    "ProxyError",
    "Request",
    "Response",
    "ResponseHead",
    "SameSite",
    "SetCookieEvent",
    "TicketCache",
    "Transport",
    "ValidationError",
    "VerifyError",
    "WebSocket",
    "WebSocketOptions",
    "__version__",
    "as_kind",
    "exact",
    "fetch_pins",
    "list_profiles",
    "machines",
    "new_alt_svc_cache",
    "new_client",
    "new_client_hint_store",
    "new_jar",
    "new_ticket_cache",
    "new_transport",
    "parse_profile",
    "profile_spec",
    "profiles",
    "with_options",
]
