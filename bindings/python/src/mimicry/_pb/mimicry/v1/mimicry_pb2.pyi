from google.protobuf import duration_pb2 as _duration_pb2
from google.protobuf import timestamp_pb2 as _timestamp_pb2
from google.protobuf.internal import containers as _containers
from google.protobuf.internal import enum_type_wrapper as _enum_type_wrapper
from google.protobuf import descriptor as _descriptor
from google.protobuf import message as _message
from collections.abc import Iterable as _Iterable, Mapping as _Mapping
from typing import ClassVar as _ClassVar, Optional as _Optional, Union as _Union

DESCRIPTOR: _descriptor.FileDescriptor

class Feature(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    FEATURE_NONE: _ClassVar[Feature]

class Kind(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    KIND_NAVIGATE: _ClassVar[Kind]
    KIND_FRAME: _ClassVar[Kind]
    KIND_SCRIPT: _ClassVar[Kind]
    KIND_ASYNC_SCRIPT: _ClassVar[Kind]
    KIND_DEFER_SCRIPT: _ClassVar[Kind]
    KIND_INJECTED_SCRIPT: _ClassVar[Kind]
    KIND_PRELOAD_SCRIPT: _ClassVar[Kind]
    KIND_STYLE: _ClassVar[Kind]
    KIND_IMAGE: _ClassVar[Kind]
    KIND_INJECTED_IMAGE: _ClassVar[Kind]
    KIND_FONT: _ClassVar[Kind]
    KIND_PRELOAD_FONT: _ClassVar[Kind]
    KIND_FETCH: _ClassVar[Kind]
    KIND_XHR: _ClassVar[Kind]
    KIND_BEACON: _ClassVar[Kind]
    KIND_PREFLIGHT: _ClassVar[Kind]

class Priority(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    PRIORITY_AUTO: _ClassVar[Priority]
    PRIORITY_HIGH: _ClassVar[Priority]
    PRIORITY_LOW: _ClassVar[Priority]

class CrossOrigin(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CROSS_ORIGIN_NONE: _ClassVar[CrossOrigin]
    CROSS_ORIGIN_ANONYMOUS: _ClassVar[CrossOrigin]
    CROSS_ORIGIN_USE_CREDENTIALS: _ClassVar[CrossOrigin]

class Credentials(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CREDENTIALS_SAME_ORIGIN: _ClassVar[Credentials]
    CREDENTIALS_INCLUDE: _ClassVar[Credentials]
    CREDENTIALS_OMIT: _ClassVar[Credentials]

class HintDelegation(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    HINT_DELEGATION_DEFAULT: _ClassVar[HintDelegation]
    HINT_DELEGATION_DELEGATED: _ClassVar[HintDelegation]
    HINT_DELEGATION_WITHHELD: _ClassVar[HintDelegation]

class MessageType(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    MESSAGE_TYPE_UNSPECIFIED: _ClassVar[MessageType]
    MESSAGE_TYPE_TEXT: _ClassVar[MessageType]
    MESSAGE_TYPE_BINARY: _ClassVar[MessageType]

class CertRole(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CERT_ROLE_LEAF: _ClassVar[CertRole]
    CERT_ROLE_INTERMEDIATE: _ClassVar[CertRole]
    CERT_ROLE_ROOT: _ClassVar[CertRole]

class SameSite(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    SAME_SITE_UNSET: _ClassVar[SameSite]
    SAME_SITE_LAX: _ClassVar[SameSite]
    SAME_SITE_STRICT: _ClassVar[SameSite]
    SAME_SITE_NONE: _ClassVar[SameSite]

class Code(int, metaclass=_enum_type_wrapper.EnumTypeWrapper):
    __slots__ = ()
    CODE_INTERNAL: _ClassVar[Code]
    CODE_NOT_MEASURED: _ClassVar[Code]
    CODE_CORS: _ClassVar[Code]
    CODE_PROXY: _ClassVar[Code]
    CODE_PIN: _ClassVar[Code]
    CODE_BLOCKED: _ClassVar[Code]
    CODE_PLACEMENT: _ClassVar[Code]
    CODE_CLOSE: _ClassVar[Code]
    CODE_VALIDATION: _ClassVar[Code]
    CODE_CANCELED: _ClassVar[Code]
    CODE_DEADLINE: _ClassVar[Code]
    CODE_NET: _ClassVar[Code]
    CODE_INVALID: _ClassVar[Code]
    CODE_VERIFY: _ClassVar[Code]
FEATURE_NONE: Feature
KIND_NAVIGATE: Kind
KIND_FRAME: Kind
KIND_SCRIPT: Kind
KIND_ASYNC_SCRIPT: Kind
KIND_DEFER_SCRIPT: Kind
KIND_INJECTED_SCRIPT: Kind
KIND_PRELOAD_SCRIPT: Kind
KIND_STYLE: Kind
KIND_IMAGE: Kind
KIND_INJECTED_IMAGE: Kind
KIND_FONT: Kind
KIND_PRELOAD_FONT: Kind
KIND_FETCH: Kind
KIND_XHR: Kind
KIND_BEACON: Kind
KIND_PREFLIGHT: Kind
PRIORITY_AUTO: Priority
PRIORITY_HIGH: Priority
PRIORITY_LOW: Priority
CROSS_ORIGIN_NONE: CrossOrigin
CROSS_ORIGIN_ANONYMOUS: CrossOrigin
CROSS_ORIGIN_USE_CREDENTIALS: CrossOrigin
CREDENTIALS_SAME_ORIGIN: Credentials
CREDENTIALS_INCLUDE: Credentials
CREDENTIALS_OMIT: Credentials
HINT_DELEGATION_DEFAULT: HintDelegation
HINT_DELEGATION_DELEGATED: HintDelegation
HINT_DELEGATION_WITHHELD: HintDelegation
MESSAGE_TYPE_UNSPECIFIED: MessageType
MESSAGE_TYPE_TEXT: MessageType
MESSAGE_TYPE_BINARY: MessageType
CERT_ROLE_LEAF: CertRole
CERT_ROLE_INTERMEDIATE: CertRole
CERT_ROLE_ROOT: CertRole
SAME_SITE_UNSET: SameSite
SAME_SITE_LAX: SameSite
SAME_SITE_STRICT: SameSite
SAME_SITE_NONE: SameSite
CODE_INTERNAL: Code
CODE_NOT_MEASURED: Code
CODE_CORS: Code
CODE_PROXY: Code
CODE_PIN: Code
CODE_BLOCKED: Code
CODE_PLACEMENT: Code
CODE_CLOSE: Code
CODE_VALIDATION: Code
CODE_CANCELED: Code
CODE_DEADLINE: Code
CODE_NET: Code
CODE_INVALID: Code
CODE_VERIFY: Code

class Call(_message.Message):
    __slots__ = ("continued", "hello", "close", "list_profiles", "profile_spec", "parse_profile", "machines", "new_client", "new_transport", "new_jar", "new_ticket_cache", "new_alt_svc_cache", "new_client_hint_store", "close_idle_connections", "do", "plan", "dial_websocket", "close_websocket", "fetch_pins", "write_json", "read_json", "write_netscape", "read_netscape", "clear", "jar_all", "jar_cookies", "jar_peek", "jar_set", "jar_set_string", "jar_set_host_string", "jar_set_cookies", "jar_clear_expired", "jar_clear_session", "jar_string", "jar_clone", "client_hints", "set_client_hints")
    CONTINUED_FIELD_NUMBER: _ClassVar[int]
    HELLO_FIELD_NUMBER: _ClassVar[int]
    CLOSE_FIELD_NUMBER: _ClassVar[int]
    LIST_PROFILES_FIELD_NUMBER: _ClassVar[int]
    PROFILE_SPEC_FIELD_NUMBER: _ClassVar[int]
    PARSE_PROFILE_FIELD_NUMBER: _ClassVar[int]
    MACHINES_FIELD_NUMBER: _ClassVar[int]
    NEW_CLIENT_FIELD_NUMBER: _ClassVar[int]
    NEW_TRANSPORT_FIELD_NUMBER: _ClassVar[int]
    NEW_JAR_FIELD_NUMBER: _ClassVar[int]
    NEW_TICKET_CACHE_FIELD_NUMBER: _ClassVar[int]
    NEW_ALT_SVC_CACHE_FIELD_NUMBER: _ClassVar[int]
    NEW_CLIENT_HINT_STORE_FIELD_NUMBER: _ClassVar[int]
    CLOSE_IDLE_CONNECTIONS_FIELD_NUMBER: _ClassVar[int]
    DO_FIELD_NUMBER: _ClassVar[int]
    PLAN_FIELD_NUMBER: _ClassVar[int]
    DIAL_WEBSOCKET_FIELD_NUMBER: _ClassVar[int]
    CLOSE_WEBSOCKET_FIELD_NUMBER: _ClassVar[int]
    FETCH_PINS_FIELD_NUMBER: _ClassVar[int]
    WRITE_JSON_FIELD_NUMBER: _ClassVar[int]
    READ_JSON_FIELD_NUMBER: _ClassVar[int]
    WRITE_NETSCAPE_FIELD_NUMBER: _ClassVar[int]
    READ_NETSCAPE_FIELD_NUMBER: _ClassVar[int]
    CLEAR_FIELD_NUMBER: _ClassVar[int]
    JAR_ALL_FIELD_NUMBER: _ClassVar[int]
    JAR_COOKIES_FIELD_NUMBER: _ClassVar[int]
    JAR_PEEK_FIELD_NUMBER: _ClassVar[int]
    JAR_SET_FIELD_NUMBER: _ClassVar[int]
    JAR_SET_STRING_FIELD_NUMBER: _ClassVar[int]
    JAR_SET_HOST_STRING_FIELD_NUMBER: _ClassVar[int]
    JAR_SET_COOKIES_FIELD_NUMBER: _ClassVar[int]
    JAR_CLEAR_EXPIRED_FIELD_NUMBER: _ClassVar[int]
    JAR_CLEAR_SESSION_FIELD_NUMBER: _ClassVar[int]
    JAR_STRING_FIELD_NUMBER: _ClassVar[int]
    JAR_CLONE_FIELD_NUMBER: _ClassVar[int]
    CLIENT_HINTS_FIELD_NUMBER: _ClassVar[int]
    SET_CLIENT_HINTS_FIELD_NUMBER: _ClassVar[int]
    continued: int
    hello: Hello
    close: Close
    list_profiles: ListProfiles
    profile_spec: ProfileSpec
    parse_profile: ParseProfile
    machines: Machines
    new_client: NewClient
    new_transport: NewTransport
    new_jar: NewJar
    new_ticket_cache: NewTicketCache
    new_alt_svc_cache: NewAltSvcCache
    new_client_hint_store: NewClientHintStore
    close_idle_connections: CloseIdleConnections
    do: Do
    plan: Plan
    dial_websocket: DialWebSocket
    close_websocket: CloseWebSocket
    fetch_pins: FetchPins
    write_json: WriteJSON
    read_json: ReadJSON
    write_netscape: WriteNetscape
    read_netscape: ReadNetscape
    clear: Clear
    jar_all: JarAll
    jar_cookies: JarCookies
    jar_peek: JarPeek
    jar_set: JarSet
    jar_set_string: JarSetString
    jar_set_host_string: JarSetHostString
    jar_set_cookies: JarSetCookies
    jar_clear_expired: JarClearExpired
    jar_clear_session: JarClearSession
    jar_string: JarString
    jar_clone: JarClone
    client_hints: ClientHints
    set_client_hints: SetClientHints
    def __init__(self, continued: _Optional[int] = ..., hello: _Optional[_Union[Hello, _Mapping]] = ..., close: _Optional[_Union[Close, _Mapping]] = ..., list_profiles: _Optional[_Union[ListProfiles, _Mapping]] = ..., profile_spec: _Optional[_Union[ProfileSpec, _Mapping]] = ..., parse_profile: _Optional[_Union[ParseProfile, _Mapping]] = ..., machines: _Optional[_Union[Machines, _Mapping]] = ..., new_client: _Optional[_Union[NewClient, _Mapping]] = ..., new_transport: _Optional[_Union[NewTransport, _Mapping]] = ..., new_jar: _Optional[_Union[NewJar, _Mapping]] = ..., new_ticket_cache: _Optional[_Union[NewTicketCache, _Mapping]] = ..., new_alt_svc_cache: _Optional[_Union[NewAltSvcCache, _Mapping]] = ..., new_client_hint_store: _Optional[_Union[NewClientHintStore, _Mapping]] = ..., close_idle_connections: _Optional[_Union[CloseIdleConnections, _Mapping]] = ..., do: _Optional[_Union[Do, _Mapping]] = ..., plan: _Optional[_Union[Plan, _Mapping]] = ..., dial_websocket: _Optional[_Union[DialWebSocket, _Mapping]] = ..., close_websocket: _Optional[_Union[CloseWebSocket, _Mapping]] = ..., fetch_pins: _Optional[_Union[FetchPins, _Mapping]] = ..., write_json: _Optional[_Union[WriteJSON, _Mapping]] = ..., read_json: _Optional[_Union[ReadJSON, _Mapping]] = ..., write_netscape: _Optional[_Union[WriteNetscape, _Mapping]] = ..., read_netscape: _Optional[_Union[ReadNetscape, _Mapping]] = ..., clear: _Optional[_Union[Clear, _Mapping]] = ..., jar_all: _Optional[_Union[JarAll, _Mapping]] = ..., jar_cookies: _Optional[_Union[JarCookies, _Mapping]] = ..., jar_peek: _Optional[_Union[JarPeek, _Mapping]] = ..., jar_set: _Optional[_Union[JarSet, _Mapping]] = ..., jar_set_string: _Optional[_Union[JarSetString, _Mapping]] = ..., jar_set_host_string: _Optional[_Union[JarSetHostString, _Mapping]] = ..., jar_set_cookies: _Optional[_Union[JarSetCookies, _Mapping]] = ..., jar_clear_expired: _Optional[_Union[JarClearExpired, _Mapping]] = ..., jar_clear_session: _Optional[_Union[JarClearSession, _Mapping]] = ..., jar_string: _Optional[_Union[JarString, _Mapping]] = ..., jar_clone: _Optional[_Union[JarClone, _Mapping]] = ..., client_hints: _Optional[_Union[ClientHints, _Mapping]] = ..., set_client_hints: _Optional[_Union[SetClientHints, _Mapping]] = ...) -> None: ...

class Reply(_message.Message):
    __slots__ = ("continued", "error", "hello", "object", "new_client", "profiles", "profile", "machines", "response", "plan", "websocket", "pins", "cookies", "count", "value", "strings")
    CONTINUED_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    HELLO_FIELD_NUMBER: _ClassVar[int]
    OBJECT_FIELD_NUMBER: _ClassVar[int]
    NEW_CLIENT_FIELD_NUMBER: _ClassVar[int]
    PROFILES_FIELD_NUMBER: _ClassVar[int]
    PROFILE_FIELD_NUMBER: _ClassVar[int]
    MACHINES_FIELD_NUMBER: _ClassVar[int]
    RESPONSE_FIELD_NUMBER: _ClassVar[int]
    PLAN_FIELD_NUMBER: _ClassVar[int]
    WEBSOCKET_FIELD_NUMBER: _ClassVar[int]
    PINS_FIELD_NUMBER: _ClassVar[int]
    COOKIES_FIELD_NUMBER: _ClassVar[int]
    COUNT_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    STRINGS_FIELD_NUMBER: _ClassVar[int]
    continued: int
    error: Error
    hello: HelloReply
    object: Object
    new_client: NewClientReply
    profiles: ProfileList
    profile: ProfileInfo
    machines: MachineList
    response: Response
    plan: PlanReply
    websocket: WebSocket
    pins: CertPins
    cookies: Cookies
    count: int
    value: bytes
    strings: Strings
    def __init__(self, continued: _Optional[int] = ..., error: _Optional[_Union[Error, _Mapping]] = ..., hello: _Optional[_Union[HelloReply, _Mapping]] = ..., object: _Optional[_Union[Object, _Mapping]] = ..., new_client: _Optional[_Union[NewClientReply, _Mapping]] = ..., profiles: _Optional[_Union[ProfileList, _Mapping]] = ..., profile: _Optional[_Union[ProfileInfo, _Mapping]] = ..., machines: _Optional[_Union[MachineList, _Mapping]] = ..., response: _Optional[_Union[Response, _Mapping]] = ..., plan: _Optional[_Union[PlanReply, _Mapping]] = ..., websocket: _Optional[_Union[WebSocket, _Mapping]] = ..., pins: _Optional[_Union[CertPins, _Mapping]] = ..., cookies: _Optional[_Union[Cookies, _Mapping]] = ..., count: _Optional[int] = ..., value: _Optional[bytes] = ..., strings: _Optional[_Union[Strings, _Mapping]] = ...) -> None: ...

class Event(_message.Message):
    __slots__ = ("continued", "verify_connection", "set_cookie", "stream_error", "websocket_written")
    CONTINUED_FIELD_NUMBER: _ClassVar[int]
    VERIFY_CONNECTION_FIELD_NUMBER: _ClassVar[int]
    SET_COOKIE_FIELD_NUMBER: _ClassVar[int]
    STREAM_ERROR_FIELD_NUMBER: _ClassVar[int]
    WEBSOCKET_WRITTEN_FIELD_NUMBER: _ClassVar[int]
    continued: int
    verify_connection: VerifyConnection
    set_cookie: SetCookie
    stream_error: StreamError
    websocket_written: WebSocketWritten
    def __init__(self, continued: _Optional[int] = ..., verify_connection: _Optional[_Union[VerifyConnection, _Mapping]] = ..., set_cookie: _Optional[_Union[SetCookie, _Mapping]] = ..., stream_error: _Optional[_Union[StreamError, _Mapping]] = ..., websocket_written: _Optional[_Union[WebSocketWritten, _Mapping]] = ...) -> None: ...

class EventReply(_message.Message):
    __slots__ = ("error",)
    ERROR_FIELD_NUMBER: _ClassVar[int]
    error: Error
    def __init__(self, error: _Optional[_Union[Error, _Mapping]] = ...) -> None: ...

class Hello(_message.Message):
    __slots__ = ("protocol_major", "protocol_minor", "sdk_version", "features")
    PROTOCOL_MAJOR_FIELD_NUMBER: _ClassVar[int]
    PROTOCOL_MINOR_FIELD_NUMBER: _ClassVar[int]
    SDK_VERSION_FIELD_NUMBER: _ClassVar[int]
    FEATURES_FIELD_NUMBER: _ClassVar[int]
    protocol_major: int
    protocol_minor: int
    sdk_version: str
    features: int
    def __init__(self, protocol_major: _Optional[int] = ..., protocol_minor: _Optional[int] = ..., sdk_version: _Optional[str] = ..., features: _Optional[int] = ...) -> None: ...

class HelloReply(_message.Message):
    __slots__ = ("protocol_major", "protocol_minor", "core_version", "features")
    PROTOCOL_MAJOR_FIELD_NUMBER: _ClassVar[int]
    PROTOCOL_MINOR_FIELD_NUMBER: _ClassVar[int]
    CORE_VERSION_FIELD_NUMBER: _ClassVar[int]
    FEATURES_FIELD_NUMBER: _ClassVar[int]
    protocol_major: int
    protocol_minor: int
    core_version: str
    features: int
    def __init__(self, protocol_major: _Optional[int] = ..., protocol_minor: _Optional[int] = ..., core_version: _Optional[str] = ..., features: _Optional[int] = ...) -> None: ...

class Close(_message.Message):
    __slots__ = ("id",)
    ID_FIELD_NUMBER: _ClassVar[int]
    id: int
    def __init__(self, id: _Optional[int] = ...) -> None: ...

class Object(_message.Message):
    __slots__ = ("id",)
    ID_FIELD_NUMBER: _ClassVar[int]
    id: int
    def __init__(self, id: _Optional[int] = ...) -> None: ...

class Strings(_message.Message):
    __slots__ = ("values",)
    VALUES_FIELD_NUMBER: _ClassVar[int]
    values: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, values: _Optional[_Iterable[str]] = ...) -> None: ...

class ListProfiles(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ProfileList(_message.Message):
    __slots__ = ("names", "supported")
    NAMES_FIELD_NUMBER: _ClassVar[int]
    SUPPORTED_FIELD_NUMBER: _ClassVar[int]
    names: _containers.RepeatedScalarFieldContainer[str]
    supported: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, names: _Optional[_Iterable[str]] = ..., supported: _Optional[_Iterable[str]] = ...) -> None: ...

class ProfileRef(_message.Message):
    __slots__ = ("name", "id")
    NAME_FIELD_NUMBER: _ClassVar[int]
    ID_FIELD_NUMBER: _ClassVar[int]
    name: str
    id: int
    def __init__(self, name: _Optional[str] = ..., id: _Optional[int] = ...) -> None: ...

class ProfileSpec(_message.Message):
    __slots__ = ("profile",)
    PROFILE_FIELD_NUMBER: _ClassVar[int]
    profile: ProfileRef
    def __init__(self, profile: _Optional[_Union[ProfileRef, _Mapping]] = ...) -> None: ...

class ParseProfile(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class ProfileInfo(_message.Message):
    __slots__ = ("id", "name", "user_agent")
    ID_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    USER_AGENT_FIELD_NUMBER: _ClassVar[int]
    id: int
    name: str
    user_agent: str
    def __init__(self, id: _Optional[int] = ..., name: _Optional[str] = ..., user_agent: _Optional[str] = ...) -> None: ...

class Machines(_message.Message):
    __slots__ = ("platform",)
    PLATFORM_FIELD_NUMBER: _ClassVar[int]
    platform: str
    def __init__(self, platform: _Optional[str] = ...) -> None: ...

class MachineList(_message.Message):
    __slots__ = ("machines",)
    MACHINES_FIELD_NUMBER: _ClassVar[int]
    machines: _containers.RepeatedScalarFieldContainer[bytes]
    def __init__(self, machines: _Optional[_Iterable[bytes]] = ...) -> None: ...

class Config(_message.Message):
    __slots__ = ("profile", "root_cas", "dial_timeout", "timeout", "local_addrs", "dns_server", "proxy", "header", "header_placement", "cookies", "client_hint_store", "client_hints", "disable_decompression", "disable_http3", "alt_svc", "tickets", "browser_quic_fallback", "browser_dial_delay", "browser_proxy_auth", "disable_coalescing", "max_conns_per_host", "idle_conn_timeout", "max_idle_proxies", "http3_over_socks5", "connect_udp_proxy", "http3_read_buffer_size", "http3_ack_every", "pins", "verify_connection", "client_certificates", "key_log_file")
    class ConnectUdpProxyEntry(_message.Message):
        __slots__ = ("key", "value")
        KEY_FIELD_NUMBER: _ClassVar[int]
        VALUE_FIELD_NUMBER: _ClassVar[int]
        key: str
        value: str
        def __init__(self, key: _Optional[str] = ..., value: _Optional[str] = ...) -> None: ...
    PROFILE_FIELD_NUMBER: _ClassVar[int]
    ROOT_CAS_FIELD_NUMBER: _ClassVar[int]
    DIAL_TIMEOUT_FIELD_NUMBER: _ClassVar[int]
    TIMEOUT_FIELD_NUMBER: _ClassVar[int]
    LOCAL_ADDRS_FIELD_NUMBER: _ClassVar[int]
    DNS_SERVER_FIELD_NUMBER: _ClassVar[int]
    PROXY_FIELD_NUMBER: _ClassVar[int]
    HEADER_FIELD_NUMBER: _ClassVar[int]
    HEADER_PLACEMENT_FIELD_NUMBER: _ClassVar[int]
    COOKIES_FIELD_NUMBER: _ClassVar[int]
    CLIENT_HINT_STORE_FIELD_NUMBER: _ClassVar[int]
    CLIENT_HINTS_FIELD_NUMBER: _ClassVar[int]
    DISABLE_DECOMPRESSION_FIELD_NUMBER: _ClassVar[int]
    DISABLE_HTTP3_FIELD_NUMBER: _ClassVar[int]
    ALT_SVC_FIELD_NUMBER: _ClassVar[int]
    TICKETS_FIELD_NUMBER: _ClassVar[int]
    BROWSER_QUIC_FALLBACK_FIELD_NUMBER: _ClassVar[int]
    BROWSER_DIAL_DELAY_FIELD_NUMBER: _ClassVar[int]
    BROWSER_PROXY_AUTH_FIELD_NUMBER: _ClassVar[int]
    DISABLE_COALESCING_FIELD_NUMBER: _ClassVar[int]
    MAX_CONNS_PER_HOST_FIELD_NUMBER: _ClassVar[int]
    IDLE_CONN_TIMEOUT_FIELD_NUMBER: _ClassVar[int]
    MAX_IDLE_PROXIES_FIELD_NUMBER: _ClassVar[int]
    HTTP3_OVER_SOCKS5_FIELD_NUMBER: _ClassVar[int]
    CONNECT_UDP_PROXY_FIELD_NUMBER: _ClassVar[int]
    HTTP3_READ_BUFFER_SIZE_FIELD_NUMBER: _ClassVar[int]
    HTTP3_ACK_EVERY_FIELD_NUMBER: _ClassVar[int]
    PINS_FIELD_NUMBER: _ClassVar[int]
    VERIFY_CONNECTION_FIELD_NUMBER: _ClassVar[int]
    CLIENT_CERTIFICATES_FIELD_NUMBER: _ClassVar[int]
    KEY_LOG_FILE_FIELD_NUMBER: _ClassVar[int]
    profile: ProfileRef
    root_cas: bytes
    dial_timeout: _duration_pb2.Duration
    timeout: _duration_pb2.Duration
    local_addrs: _containers.RepeatedScalarFieldContainer[str]
    dns_server: str
    proxy: str
    header: _containers.RepeatedCompositeFieldContainer[HeaderValues]
    header_placement: _containers.RepeatedCompositeFieldContainer[Place]
    cookies: int
    client_hint_store: int
    client_hints: bytes
    disable_decompression: bool
    disable_http3: bool
    alt_svc: int
    tickets: int
    browser_quic_fallback: bool
    browser_dial_delay: bool
    browser_proxy_auth: bool
    disable_coalescing: bool
    max_conns_per_host: int
    idle_conn_timeout: _duration_pb2.Duration
    max_idle_proxies: int
    http3_over_socks5: bool
    connect_udp_proxy: _containers.ScalarMap[str, str]
    http3_read_buffer_size: int
    http3_ack_every: int
    pins: _containers.RepeatedCompositeFieldContainer[Pin]
    verify_connection: bool
    client_certificates: _containers.RepeatedCompositeFieldContainer[ClientCertificate]
    key_log_file: str
    def __init__(self, profile: _Optional[_Union[ProfileRef, _Mapping]] = ..., root_cas: _Optional[bytes] = ..., dial_timeout: _Optional[_Union[_duration_pb2.Duration, _Mapping]] = ..., timeout: _Optional[_Union[_duration_pb2.Duration, _Mapping]] = ..., local_addrs: _Optional[_Iterable[str]] = ..., dns_server: _Optional[str] = ..., proxy: _Optional[str] = ..., header: _Optional[_Iterable[_Union[HeaderValues, _Mapping]]] = ..., header_placement: _Optional[_Iterable[_Union[Place, _Mapping]]] = ..., cookies: _Optional[int] = ..., client_hint_store: _Optional[int] = ..., client_hints: _Optional[bytes] = ..., disable_decompression: bool = ..., disable_http3: bool = ..., alt_svc: _Optional[int] = ..., tickets: _Optional[int] = ..., browser_quic_fallback: bool = ..., browser_dial_delay: bool = ..., browser_proxy_auth: bool = ..., disable_coalescing: bool = ..., max_conns_per_host: _Optional[int] = ..., idle_conn_timeout: _Optional[_Union[_duration_pb2.Duration, _Mapping]] = ..., max_idle_proxies: _Optional[int] = ..., http3_over_socks5: bool = ..., connect_udp_proxy: _Optional[_Mapping[str, str]] = ..., http3_read_buffer_size: _Optional[int] = ..., http3_ack_every: _Optional[int] = ..., pins: _Optional[_Iterable[_Union[Pin, _Mapping]]] = ..., verify_connection: bool = ..., client_certificates: _Optional[_Iterable[_Union[ClientCertificate, _Mapping]]] = ..., key_log_file: _Optional[str] = ...) -> None: ...

class HeaderValues(_message.Message):
    __slots__ = ("name", "values")
    NAME_FIELD_NUMBER: _ClassVar[int]
    VALUES_FIELD_NUMBER: _ClassVar[int]
    name: str
    values: _containers.RepeatedScalarFieldContainer[bytes]
    def __init__(self, name: _Optional[str] = ..., values: _Optional[_Iterable[bytes]] = ...) -> None: ...

class Header(_message.Message):
    __slots__ = ("name", "value")
    NAME_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    name: str
    value: bytes
    def __init__(self, name: _Optional[str] = ..., value: _Optional[bytes] = ...) -> None: ...

class Place(_message.Message):
    __slots__ = ("header", "after", "before")
    HEADER_FIELD_NUMBER: _ClassVar[int]
    AFTER_FIELD_NUMBER: _ClassVar[int]
    BEFORE_FIELD_NUMBER: _ClassVar[int]
    header: str
    after: str
    before: str
    def __init__(self, header: _Optional[str] = ..., after: _Optional[str] = ..., before: _Optional[str] = ...) -> None: ...

class Pin(_message.Message):
    __slots__ = ("host", "include_subdomains", "spki")
    HOST_FIELD_NUMBER: _ClassVar[int]
    INCLUDE_SUBDOMAINS_FIELD_NUMBER: _ClassVar[int]
    SPKI_FIELD_NUMBER: _ClassVar[int]
    host: str
    include_subdomains: bool
    spki: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, host: _Optional[str] = ..., include_subdomains: bool = ..., spki: _Optional[_Iterable[str]] = ...) -> None: ...

class ClientCertificate(_message.Message):
    __slots__ = ("host", "certificate", "key")
    HOST_FIELD_NUMBER: _ClassVar[int]
    CERTIFICATE_FIELD_NUMBER: _ClassVar[int]
    KEY_FIELD_NUMBER: _ClassVar[int]
    host: str
    certificate: bytes
    key: bytes
    def __init__(self, host: _Optional[str] = ..., certificate: _Optional[bytes] = ..., key: _Optional[bytes] = ...) -> None: ...

class NewClient(_message.Message):
    __slots__ = ("config", "redirects")
    CONFIG_FIELD_NUMBER: _ClassVar[int]
    REDIRECTS_FIELD_NUMBER: _ClassVar[int]
    config: Config
    redirects: Redirects
    def __init__(self, config: _Optional[_Union[Config, _Mapping]] = ..., redirects: _Optional[_Union[Redirects, _Mapping]] = ...) -> None: ...

class Redirects(_message.Message):
    __slots__ = ("follow", "max")
    FOLLOW_FIELD_NUMBER: _ClassVar[int]
    MAX_FIELD_NUMBER: _ClassVar[int]
    follow: bool
    max: int
    def __init__(self, follow: bool = ..., max: _Optional[int] = ...) -> None: ...

class NewClientReply(_message.Message):
    __slots__ = ("client", "cookies")
    CLIENT_FIELD_NUMBER: _ClassVar[int]
    COOKIES_FIELD_NUMBER: _ClassVar[int]
    client: int
    cookies: int
    def __init__(self, client: _Optional[int] = ..., cookies: _Optional[int] = ...) -> None: ...

class NewTransport(_message.Message):
    __slots__ = ("config",)
    CONFIG_FIELD_NUMBER: _ClassVar[int]
    config: Config
    def __init__(self, config: _Optional[_Union[Config, _Mapping]] = ...) -> None: ...

class NewJar(_message.Message):
    __slots__ = ("rules", "on_set_cookie")
    RULES_FIELD_NUMBER: _ClassVar[int]
    ON_SET_COOKIE_FIELD_NUMBER: _ClassVar[int]
    rules: ProfileRef
    on_set_cookie: bool
    def __init__(self, rules: _Optional[_Union[ProfileRef, _Mapping]] = ..., on_set_cookie: bool = ...) -> None: ...

class NewTicketCache(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class NewAltSvcCache(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class NewClientHintStore(_message.Message):
    __slots__ = ()
    def __init__(self) -> None: ...

class CloseIdleConnections(_message.Message):
    __slots__ = ("transport",)
    TRANSPORT_FIELD_NUMBER: _ClassVar[int]
    transport: int
    def __init__(self, transport: _Optional[int] = ...) -> None: ...

class Options(_message.Message):
    __slots__ = ("kind", "page", "priority", "cross_origin", "credentials", "no_cors", "stylesheet", "ignore_cors", "header_placement", "client_hints")
    KIND_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    PRIORITY_FIELD_NUMBER: _ClassVar[int]
    CROSS_ORIGIN_FIELD_NUMBER: _ClassVar[int]
    CREDENTIALS_FIELD_NUMBER: _ClassVar[int]
    NO_CORS_FIELD_NUMBER: _ClassVar[int]
    STYLESHEET_FIELD_NUMBER: _ClassVar[int]
    IGNORE_CORS_FIELD_NUMBER: _ClassVar[int]
    HEADER_PLACEMENT_FIELD_NUMBER: _ClassVar[int]
    CLIENT_HINTS_FIELD_NUMBER: _ClassVar[int]
    kind: Kind
    page: str
    priority: Priority
    cross_origin: CrossOrigin
    credentials: Credentials
    no_cors: bool
    stylesheet: str
    ignore_cors: bool
    header_placement: _containers.RepeatedCompositeFieldContainer[Place]
    client_hints: HintDelegation
    def __init__(self, kind: _Optional[_Union[Kind, str]] = ..., page: _Optional[str] = ..., priority: _Optional[_Union[Priority, str]] = ..., cross_origin: _Optional[_Union[CrossOrigin, str]] = ..., credentials: _Optional[_Union[Credentials, str]] = ..., no_cors: bool = ..., stylesheet: _Optional[str] = ..., ignore_cors: bool = ..., header_placement: _Optional[_Iterable[_Union[Place, _Mapping]]] = ..., client_hints: _Optional[_Union[HintDelegation, str]] = ...) -> None: ...

class Request(_message.Message):
    __slots__ = ("method", "url", "header", "body", "stream_body", "content_length", "options", "exact", "header_order", "proxy", "timeout")
    METHOD_FIELD_NUMBER: _ClassVar[int]
    URL_FIELD_NUMBER: _ClassVar[int]
    HEADER_FIELD_NUMBER: _ClassVar[int]
    BODY_FIELD_NUMBER: _ClassVar[int]
    STREAM_BODY_FIELD_NUMBER: _ClassVar[int]
    CONTENT_LENGTH_FIELD_NUMBER: _ClassVar[int]
    OPTIONS_FIELD_NUMBER: _ClassVar[int]
    EXACT_FIELD_NUMBER: _ClassVar[int]
    HEADER_ORDER_FIELD_NUMBER: _ClassVar[int]
    PROXY_FIELD_NUMBER: _ClassVar[int]
    TIMEOUT_FIELD_NUMBER: _ClassVar[int]
    method: str
    url: str
    header: _containers.RepeatedCompositeFieldContainer[HeaderValues]
    body: bytes
    stream_body: bool
    content_length: int
    options: Options
    exact: bool
    header_order: _containers.RepeatedScalarFieldContainer[str]
    proxy: str
    timeout: _duration_pb2.Duration
    def __init__(self, method: _Optional[str] = ..., url: _Optional[str] = ..., header: _Optional[_Iterable[_Union[HeaderValues, _Mapping]]] = ..., body: _Optional[bytes] = ..., stream_body: bool = ..., content_length: _Optional[int] = ..., options: _Optional[_Union[Options, _Mapping]] = ..., exact: bool = ..., header_order: _Optional[_Iterable[str]] = ..., proxy: _Optional[str] = ..., timeout: _Optional[_Union[_duration_pb2.Duration, _Mapping]] = ...) -> None: ...

class Do(_message.Message):
    __slots__ = ("client", "request")
    CLIENT_FIELD_NUMBER: _ClassVar[int]
    REQUEST_FIELD_NUMBER: _ClassVar[int]
    client: int
    request: Request
    def __init__(self, client: _Optional[int] = ..., request: _Optional[_Union[Request, _Mapping]] = ...) -> None: ...

class Response(_message.Message):
    __slots__ = ("status_code", "status", "proto", "header", "content_length", "uncompressed", "url", "tls")
    STATUS_CODE_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    PROTO_FIELD_NUMBER: _ClassVar[int]
    HEADER_FIELD_NUMBER: _ClassVar[int]
    CONTENT_LENGTH_FIELD_NUMBER: _ClassVar[int]
    UNCOMPRESSED_FIELD_NUMBER: _ClassVar[int]
    URL_FIELD_NUMBER: _ClassVar[int]
    TLS_FIELD_NUMBER: _ClassVar[int]
    status_code: int
    status: str
    proto: str
    header: _containers.RepeatedCompositeFieldContainer[HeaderValues]
    content_length: int
    uncompressed: bool
    url: str
    tls: ConnectionState
    def __init__(self, status_code: _Optional[int] = ..., status: _Optional[str] = ..., proto: _Optional[str] = ..., header: _Optional[_Iterable[_Union[HeaderValues, _Mapping]]] = ..., content_length: _Optional[int] = ..., uncompressed: bool = ..., url: _Optional[str] = ..., tls: _Optional[_Union[ConnectionState, _Mapping]] = ...) -> None: ...

class ConnectionState(_message.Message):
    __slots__ = ("version", "handshake_complete", "did_resume", "cipher_suite", "negotiated_protocol", "server_name", "peer_certificates", "verified_chains", "signed_certificate_timestamps", "ocsp_response", "ech_accepted")
    VERSION_FIELD_NUMBER: _ClassVar[int]
    HANDSHAKE_COMPLETE_FIELD_NUMBER: _ClassVar[int]
    DID_RESUME_FIELD_NUMBER: _ClassVar[int]
    CIPHER_SUITE_FIELD_NUMBER: _ClassVar[int]
    NEGOTIATED_PROTOCOL_FIELD_NUMBER: _ClassVar[int]
    SERVER_NAME_FIELD_NUMBER: _ClassVar[int]
    PEER_CERTIFICATES_FIELD_NUMBER: _ClassVar[int]
    VERIFIED_CHAINS_FIELD_NUMBER: _ClassVar[int]
    SIGNED_CERTIFICATE_TIMESTAMPS_FIELD_NUMBER: _ClassVar[int]
    OCSP_RESPONSE_FIELD_NUMBER: _ClassVar[int]
    ECH_ACCEPTED_FIELD_NUMBER: _ClassVar[int]
    version: int
    handshake_complete: bool
    did_resume: bool
    cipher_suite: int
    negotiated_protocol: str
    server_name: str
    peer_certificates: _containers.RepeatedScalarFieldContainer[bytes]
    verified_chains: _containers.RepeatedCompositeFieldContainer[CertificateChain]
    signed_certificate_timestamps: _containers.RepeatedScalarFieldContainer[bytes]
    ocsp_response: bytes
    ech_accepted: bool
    def __init__(self, version: _Optional[int] = ..., handshake_complete: bool = ..., did_resume: bool = ..., cipher_suite: _Optional[int] = ..., negotiated_protocol: _Optional[str] = ..., server_name: _Optional[str] = ..., peer_certificates: _Optional[_Iterable[bytes]] = ..., verified_chains: _Optional[_Iterable[_Union[CertificateChain, _Mapping]]] = ..., signed_certificate_timestamps: _Optional[_Iterable[bytes]] = ..., ocsp_response: _Optional[bytes] = ..., ech_accepted: bool = ...) -> None: ...

class CertificateChain(_message.Message):
    __slots__ = ("certificates",)
    CERTIFICATES_FIELD_NUMBER: _ClassVar[int]
    certificates: _containers.RepeatedScalarFieldContainer[bytes]
    def __init__(self, certificates: _Optional[_Iterable[bytes]] = ...) -> None: ...

class Plan(_message.Message):
    __slots__ = ("transport", "request")
    TRANSPORT_FIELD_NUMBER: _ClassVar[int]
    REQUEST_FIELD_NUMBER: _ClassVar[int]
    transport: int
    request: Request
    def __init__(self, transport: _Optional[int] = ..., request: _Optional[_Union[Request, _Mapping]] = ...) -> None: ...

class PlanReply(_message.Message):
    __slots__ = ("method", "url", "kind", "variant", "condition", "protocol", "http2", "http1", "http3", "preflight", "overrides", "dropped", "text")
    METHOD_FIELD_NUMBER: _ClassVar[int]
    URL_FIELD_NUMBER: _ClassVar[int]
    KIND_FIELD_NUMBER: _ClassVar[int]
    VARIANT_FIELD_NUMBER: _ClassVar[int]
    CONDITION_FIELD_NUMBER: _ClassVar[int]
    PROTOCOL_FIELD_NUMBER: _ClassVar[int]
    HTTP2_FIELD_NUMBER: _ClassVar[int]
    HTTP1_FIELD_NUMBER: _ClassVar[int]
    HTTP3_FIELD_NUMBER: _ClassVar[int]
    PREFLIGHT_FIELD_NUMBER: _ClassVar[int]
    OVERRIDES_FIELD_NUMBER: _ClassVar[int]
    DROPPED_FIELD_NUMBER: _ClassVar[int]
    TEXT_FIELD_NUMBER: _ClassVar[int]
    method: str
    url: str
    kind: Kind
    variant: str
    condition: _containers.RepeatedScalarFieldContainer[str]
    protocol: str
    http2: Lines
    http1: Lines
    http3: Lines
    preflight: PlanReply
    overrides: _containers.RepeatedCompositeFieldContainer[Override]
    dropped: _containers.RepeatedScalarFieldContainer[str]
    text: str
    def __init__(self, method: _Optional[str] = ..., url: _Optional[str] = ..., kind: _Optional[_Union[Kind, str]] = ..., variant: _Optional[str] = ..., condition: _Optional[_Iterable[str]] = ..., protocol: _Optional[str] = ..., http2: _Optional[_Union[Lines, _Mapping]] = ..., http1: _Optional[_Union[Lines, _Mapping]] = ..., http3: _Optional[_Union[Lines, _Mapping]] = ..., preflight: _Optional[_Union[PlanReply, _Mapping]] = ..., overrides: _Optional[_Iterable[_Union[Override, _Mapping]]] = ..., dropped: _Optional[_Iterable[str]] = ..., text: _Optional[str] = ...) -> None: ...

class Lines(_message.Message):
    __slots__ = ("request_line", "headers", "error")
    REQUEST_LINE_FIELD_NUMBER: _ClassVar[int]
    HEADERS_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    request_line: str
    headers: _containers.RepeatedCompositeFieldContainer[Header]
    error: Error
    def __init__(self, request_line: _Optional[str] = ..., headers: _Optional[_Iterable[_Union[Header, _Mapping]]] = ..., error: _Optional[_Union[Error, _Mapping]] = ...) -> None: ...

class Override(_message.Message):
    __slots__ = ("name", "mimicry", "caller")
    NAME_FIELD_NUMBER: _ClassVar[int]
    MIMICRY_FIELD_NUMBER: _ClassVar[int]
    CALLER_FIELD_NUMBER: _ClassVar[int]
    name: str
    mimicry: bytes
    caller: _containers.RepeatedScalarFieldContainer[bytes]
    def __init__(self, name: _Optional[str] = ..., mimicry: _Optional[bytes] = ..., caller: _Optional[_Iterable[bytes]] = ...) -> None: ...

class DialWebSocket(_message.Message):
    __slots__ = ("transport", "url", "options", "timeout")
    TRANSPORT_FIELD_NUMBER: _ClassVar[int]
    URL_FIELD_NUMBER: _ClassVar[int]
    OPTIONS_FIELD_NUMBER: _ClassVar[int]
    TIMEOUT_FIELD_NUMBER: _ClassVar[int]
    transport: int
    url: str
    options: WebSocketOptions
    timeout: _duration_pb2.Duration
    def __init__(self, transport: _Optional[int] = ..., url: _Optional[str] = ..., options: _Optional[_Union[WebSocketOptions, _Mapping]] = ..., timeout: _Optional[_Union[_duration_pb2.Duration, _Mapping]] = ...) -> None: ...

class WebSocketOptions(_message.Message):
    __slots__ = ("page", "protocols", "header")
    PAGE_FIELD_NUMBER: _ClassVar[int]
    PROTOCOLS_FIELD_NUMBER: _ClassVar[int]
    HEADER_FIELD_NUMBER: _ClassVar[int]
    page: str
    protocols: _containers.RepeatedScalarFieldContainer[str]
    header: _containers.RepeatedCompositeFieldContainer[HeaderValues]
    def __init__(self, page: _Optional[str] = ..., protocols: _Optional[_Iterable[str]] = ..., header: _Optional[_Iterable[_Union[HeaderValues, _Mapping]]] = ...) -> None: ...

class WebSocket(_message.Message):
    __slots__ = ("id", "protocol", "extensions", "response")
    ID_FIELD_NUMBER: _ClassVar[int]
    PROTOCOL_FIELD_NUMBER: _ClassVar[int]
    EXTENSIONS_FIELD_NUMBER: _ClassVar[int]
    RESPONSE_FIELD_NUMBER: _ClassVar[int]
    id: int
    protocol: str
    extensions: str
    response: Response
    def __init__(self, id: _Optional[int] = ..., protocol: _Optional[str] = ..., extensions: _Optional[str] = ..., response: _Optional[_Union[Response, _Mapping]] = ...) -> None: ...

class CloseWebSocket(_message.Message):
    __slots__ = ("websocket", "code", "reason")
    WEBSOCKET_FIELD_NUMBER: _ClassVar[int]
    CODE_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    websocket: int
    code: int
    reason: str
    def __init__(self, websocket: _Optional[int] = ..., code: _Optional[int] = ..., reason: _Optional[str] = ...) -> None: ...

class WebSocketWritten(_message.Message):
    __slots__ = ("websocket", "seq", "error")
    WEBSOCKET_FIELD_NUMBER: _ClassVar[int]
    SEQ_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    websocket: int
    seq: int
    error: Error
    def __init__(self, websocket: _Optional[int] = ..., seq: _Optional[int] = ..., error: _Optional[_Union[Error, _Mapping]] = ...) -> None: ...

class FetchPins(_message.Message):
    __slots__ = ("config", "url", "timeout")
    CONFIG_FIELD_NUMBER: _ClassVar[int]
    URL_FIELD_NUMBER: _ClassVar[int]
    TIMEOUT_FIELD_NUMBER: _ClassVar[int]
    config: Config
    url: str
    timeout: _duration_pb2.Duration
    def __init__(self, config: _Optional[_Union[Config, _Mapping]] = ..., url: _Optional[str] = ..., timeout: _Optional[_Union[_duration_pb2.Duration, _Mapping]] = ...) -> None: ...

class CertPin(_message.Message):
    __slots__ = ("role", "subject", "issuer", "not_after", "spki", "spki_hex", "fingerprint")
    ROLE_FIELD_NUMBER: _ClassVar[int]
    SUBJECT_FIELD_NUMBER: _ClassVar[int]
    ISSUER_FIELD_NUMBER: _ClassVar[int]
    NOT_AFTER_FIELD_NUMBER: _ClassVar[int]
    SPKI_FIELD_NUMBER: _ClassVar[int]
    SPKI_HEX_FIELD_NUMBER: _ClassVar[int]
    FINGERPRINT_FIELD_NUMBER: _ClassVar[int]
    role: CertRole
    subject: str
    issuer: str
    not_after: _timestamp_pb2.Timestamp
    spki: str
    spki_hex: str
    fingerprint: str
    def __init__(self, role: _Optional[_Union[CertRole, str]] = ..., subject: _Optional[str] = ..., issuer: _Optional[str] = ..., not_after: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ..., spki: _Optional[str] = ..., spki_hex: _Optional[str] = ..., fingerprint: _Optional[str] = ...) -> None: ...

class CertPins(_message.Message):
    __slots__ = ("pins",)
    PINS_FIELD_NUMBER: _ClassVar[int]
    pins: _containers.RepeatedCompositeFieldContainer[CertPin]
    def __init__(self, pins: _Optional[_Iterable[_Union[CertPin, _Mapping]]] = ...) -> None: ...

class WriteJSON(_message.Message):
    __slots__ = ("id",)
    ID_FIELD_NUMBER: _ClassVar[int]
    id: int
    def __init__(self, id: _Optional[int] = ...) -> None: ...

class ReadJSON(_message.Message):
    __slots__ = ("id",)
    ID_FIELD_NUMBER: _ClassVar[int]
    id: int
    def __init__(self, id: _Optional[int] = ...) -> None: ...

class WriteNetscape(_message.Message):
    __slots__ = ("jar",)
    JAR_FIELD_NUMBER: _ClassVar[int]
    jar: int
    def __init__(self, jar: _Optional[int] = ...) -> None: ...

class ReadNetscape(_message.Message):
    __slots__ = ("jar",)
    JAR_FIELD_NUMBER: _ClassVar[int]
    jar: int
    def __init__(self, jar: _Optional[int] = ...) -> None: ...

class Clear(_message.Message):
    __slots__ = ("id",)
    ID_FIELD_NUMBER: _ClassVar[int]
    id: int
    def __init__(self, id: _Optional[int] = ...) -> None: ...

class Cookie(_message.Message):
    __slots__ = ("name", "value", "domain", "host_only", "path", "expires", "secure", "http_only", "same_site", "partitioned", "partition", "created", "last_access", "raw")
    NAME_FIELD_NUMBER: _ClassVar[int]
    VALUE_FIELD_NUMBER: _ClassVar[int]
    DOMAIN_FIELD_NUMBER: _ClassVar[int]
    HOST_ONLY_FIELD_NUMBER: _ClassVar[int]
    PATH_FIELD_NUMBER: _ClassVar[int]
    EXPIRES_FIELD_NUMBER: _ClassVar[int]
    SECURE_FIELD_NUMBER: _ClassVar[int]
    HTTP_ONLY_FIELD_NUMBER: _ClassVar[int]
    SAME_SITE_FIELD_NUMBER: _ClassVar[int]
    PARTITIONED_FIELD_NUMBER: _ClassVar[int]
    PARTITION_FIELD_NUMBER: _ClassVar[int]
    CREATED_FIELD_NUMBER: _ClassVar[int]
    LAST_ACCESS_FIELD_NUMBER: _ClassVar[int]
    RAW_FIELD_NUMBER: _ClassVar[int]
    name: bytes
    value: bytes
    domain: str
    host_only: bool
    path: str
    expires: _timestamp_pb2.Timestamp
    secure: bool
    http_only: bool
    same_site: SameSite
    partitioned: bool
    partition: str
    created: _timestamp_pb2.Timestamp
    last_access: _timestamp_pb2.Timestamp
    raw: bytes
    def __init__(self, name: _Optional[bytes] = ..., value: _Optional[bytes] = ..., domain: _Optional[str] = ..., host_only: bool = ..., path: _Optional[str] = ..., expires: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ..., secure: bool = ..., http_only: bool = ..., same_site: _Optional[_Union[SameSite, str]] = ..., partitioned: bool = ..., partition: _Optional[str] = ..., created: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ..., last_access: _Optional[_Union[_timestamp_pb2.Timestamp, _Mapping]] = ..., raw: _Optional[bytes] = ...) -> None: ...

class Cookies(_message.Message):
    __slots__ = ("cookies",)
    COOKIES_FIELD_NUMBER: _ClassVar[int]
    cookies: _containers.RepeatedCompositeFieldContainer[Cookie]
    def __init__(self, cookies: _Optional[_Iterable[_Union[Cookie, _Mapping]]] = ...) -> None: ...

class CookieRequest(_message.Message):
    __slots__ = ("url", "method", "navigation", "page", "redirects", "websocket")
    URL_FIELD_NUMBER: _ClassVar[int]
    METHOD_FIELD_NUMBER: _ClassVar[int]
    NAVIGATION_FIELD_NUMBER: _ClassVar[int]
    PAGE_FIELD_NUMBER: _ClassVar[int]
    REDIRECTS_FIELD_NUMBER: _ClassVar[int]
    WEBSOCKET_FIELD_NUMBER: _ClassVar[int]
    url: str
    method: str
    navigation: bool
    page: str
    redirects: _containers.RepeatedScalarFieldContainer[str]
    websocket: bool
    def __init__(self, url: _Optional[str] = ..., method: _Optional[str] = ..., navigation: bool = ..., page: _Optional[str] = ..., redirects: _Optional[_Iterable[str]] = ..., websocket: bool = ...) -> None: ...

class JarAll(_message.Message):
    __slots__ = ("jar",)
    JAR_FIELD_NUMBER: _ClassVar[int]
    jar: int
    def __init__(self, jar: _Optional[int] = ...) -> None: ...

class JarCookies(_message.Message):
    __slots__ = ("jar", "request")
    JAR_FIELD_NUMBER: _ClassVar[int]
    REQUEST_FIELD_NUMBER: _ClassVar[int]
    jar: int
    request: CookieRequest
    def __init__(self, jar: _Optional[int] = ..., request: _Optional[_Union[CookieRequest, _Mapping]] = ...) -> None: ...

class JarPeek(_message.Message):
    __slots__ = ("jar", "request")
    JAR_FIELD_NUMBER: _ClassVar[int]
    REQUEST_FIELD_NUMBER: _ClassVar[int]
    jar: int
    request: CookieRequest
    def __init__(self, jar: _Optional[int] = ..., request: _Optional[_Union[CookieRequest, _Mapping]] = ...) -> None: ...

class JarSet(_message.Message):
    __slots__ = ("jar", "cookie")
    JAR_FIELD_NUMBER: _ClassVar[int]
    COOKIE_FIELD_NUMBER: _ClassVar[int]
    jar: int
    cookie: Cookie
    def __init__(self, jar: _Optional[int] = ..., cookie: _Optional[_Union[Cookie, _Mapping]] = ...) -> None: ...

class JarSetString(_message.Message):
    __slots__ = ("jar", "domain", "cookies")
    JAR_FIELD_NUMBER: _ClassVar[int]
    DOMAIN_FIELD_NUMBER: _ClassVar[int]
    COOKIES_FIELD_NUMBER: _ClassVar[int]
    jar: int
    domain: str
    cookies: bytes
    def __init__(self, jar: _Optional[int] = ..., domain: _Optional[str] = ..., cookies: _Optional[bytes] = ...) -> None: ...

class JarSetHostString(_message.Message):
    __slots__ = ("jar", "host", "cookies")
    JAR_FIELD_NUMBER: _ClassVar[int]
    HOST_FIELD_NUMBER: _ClassVar[int]
    COOKIES_FIELD_NUMBER: _ClassVar[int]
    jar: int
    host: str
    cookies: bytes
    def __init__(self, jar: _Optional[int] = ..., host: _Optional[str] = ..., cookies: _Optional[bytes] = ...) -> None: ...

class JarSetCookies(_message.Message):
    __slots__ = ("jar", "request", "lines")
    JAR_FIELD_NUMBER: _ClassVar[int]
    REQUEST_FIELD_NUMBER: _ClassVar[int]
    LINES_FIELD_NUMBER: _ClassVar[int]
    jar: int
    request: CookieRequest
    lines: _containers.RepeatedScalarFieldContainer[bytes]
    def __init__(self, jar: _Optional[int] = ..., request: _Optional[_Union[CookieRequest, _Mapping]] = ..., lines: _Optional[_Iterable[bytes]] = ...) -> None: ...

class JarClearExpired(_message.Message):
    __slots__ = ("jar",)
    JAR_FIELD_NUMBER: _ClassVar[int]
    jar: int
    def __init__(self, jar: _Optional[int] = ...) -> None: ...

class JarClearSession(_message.Message):
    __slots__ = ("jar",)
    JAR_FIELD_NUMBER: _ClassVar[int]
    jar: int
    def __init__(self, jar: _Optional[int] = ...) -> None: ...

class JarString(_message.Message):
    __slots__ = ("jar", "name")
    JAR_FIELD_NUMBER: _ClassVar[int]
    NAME_FIELD_NUMBER: _ClassVar[int]
    jar: int
    name: str
    def __init__(self, jar: _Optional[int] = ..., name: _Optional[str] = ...) -> None: ...

class JarClone(_message.Message):
    __slots__ = ("jar",)
    JAR_FIELD_NUMBER: _ClassVar[int]
    jar: int
    def __init__(self, jar: _Optional[int] = ...) -> None: ...

class SetCookie(_message.Message):
    __slots__ = ("jar", "url", "line", "cookie", "rejected", "deleted")
    JAR_FIELD_NUMBER: _ClassVar[int]
    URL_FIELD_NUMBER: _ClassVar[int]
    LINE_FIELD_NUMBER: _ClassVar[int]
    COOKIE_FIELD_NUMBER: _ClassVar[int]
    REJECTED_FIELD_NUMBER: _ClassVar[int]
    DELETED_FIELD_NUMBER: _ClassVar[int]
    jar: int
    url: str
    line: bytes
    cookie: Cookie
    rejected: str
    deleted: bool
    def __init__(self, jar: _Optional[int] = ..., url: _Optional[str] = ..., line: _Optional[bytes] = ..., cookie: _Optional[_Union[Cookie, _Mapping]] = ..., rejected: _Optional[str] = ..., deleted: bool = ...) -> None: ...

class ClientHints(_message.Message):
    __slots__ = ("store", "origin")
    STORE_FIELD_NUMBER: _ClassVar[int]
    ORIGIN_FIELD_NUMBER: _ClassVar[int]
    store: int
    origin: str
    def __init__(self, store: _Optional[int] = ..., origin: _Optional[str] = ...) -> None: ...

class SetClientHints(_message.Message):
    __slots__ = ("store", "origin", "hints")
    STORE_FIELD_NUMBER: _ClassVar[int]
    ORIGIN_FIELD_NUMBER: _ClassVar[int]
    HINTS_FIELD_NUMBER: _ClassVar[int]
    store: int
    origin: str
    hints: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, store: _Optional[int] = ..., origin: _Optional[str] = ..., hints: _Optional[_Iterable[str]] = ...) -> None: ...

class VerifyConnection(_message.Message):
    __slots__ = ("transport", "state")
    TRANSPORT_FIELD_NUMBER: _ClassVar[int]
    STATE_FIELD_NUMBER: _ClassVar[int]
    transport: int
    state: ConnectionState
    def __init__(self, transport: _Optional[int] = ..., state: _Optional[_Union[ConnectionState, _Mapping]] = ...) -> None: ...

class StreamError(_message.Message):
    __slots__ = ("stream", "error")
    STREAM_FIELD_NUMBER: _ClassVar[int]
    ERROR_FIELD_NUMBER: _ClassVar[int]
    stream: int
    error: Error
    def __init__(self, stream: _Optional[int] = ..., error: _Optional[_Union[Error, _Mapping]] = ...) -> None: ...

class Error(_message.Message):
    __slots__ = ("code", "message", "matches", "cors", "proxy", "pin", "close", "validation")
    CODE_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    MATCHES_FIELD_NUMBER: _ClassVar[int]
    CORS_FIELD_NUMBER: _ClassVar[int]
    PROXY_FIELD_NUMBER: _ClassVar[int]
    PIN_FIELD_NUMBER: _ClassVar[int]
    CLOSE_FIELD_NUMBER: _ClassVar[int]
    VALIDATION_FIELD_NUMBER: _ClassVar[int]
    code: Code
    message: str
    matches: _containers.RepeatedScalarFieldContainer[Code]
    cors: CORSError
    proxy: ProxyError
    pin: PinError
    close: CloseError
    validation: ValidationError
    def __init__(self, code: _Optional[_Union[Code, str]] = ..., message: _Optional[str] = ..., matches: _Optional[_Iterable[_Union[Code, str]]] = ..., cors: _Optional[_Union[CORSError, _Mapping]] = ..., proxy: _Optional[_Union[ProxyError, _Mapping]] = ..., pin: _Optional[_Union[PinError, _Mapping]] = ..., close: _Optional[_Union[CloseError, _Mapping]] = ..., validation: _Optional[_Union[ValidationError, _Mapping]] = ...) -> None: ...

class CORSError(_message.Message):
    __slots__ = ("url", "redirect", "reason", "status", "header")
    URL_FIELD_NUMBER: _ClassVar[int]
    REDIRECT_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    STATUS_FIELD_NUMBER: _ClassVar[int]
    HEADER_FIELD_NUMBER: _ClassVar[int]
    url: str
    redirect: bool
    reason: str
    status: int
    header: _containers.RepeatedCompositeFieldContainer[HeaderValues]
    def __init__(self, url: _Optional[str] = ..., redirect: bool = ..., reason: _Optional[str] = ..., status: _Optional[int] = ..., header: _Optional[_Iterable[_Union[HeaderValues, _Mapping]]] = ...) -> None: ...

class ProxyError(_message.Message):
    __slots__ = ("proxy", "status_code", "header")
    PROXY_FIELD_NUMBER: _ClassVar[int]
    STATUS_CODE_FIELD_NUMBER: _ClassVar[int]
    HEADER_FIELD_NUMBER: _ClassVar[int]
    proxy: str
    status_code: int
    header: _containers.RepeatedCompositeFieldContainer[HeaderValues]
    def __init__(self, proxy: _Optional[str] = ..., status_code: _Optional[int] = ..., header: _Optional[_Iterable[_Union[HeaderValues, _Mapping]]] = ...) -> None: ...

class PinError(_message.Message):
    __slots__ = ("host", "presented")
    HOST_FIELD_NUMBER: _ClassVar[int]
    PRESENTED_FIELD_NUMBER: _ClassVar[int]
    host: str
    presented: _containers.RepeatedScalarFieldContainer[str]
    def __init__(self, host: _Optional[str] = ..., presented: _Optional[_Iterable[str]] = ...) -> None: ...

class CloseError(_message.Message):
    __slots__ = ("code", "reason")
    CODE_FIELD_NUMBER: _ClassVar[int]
    REASON_FIELD_NUMBER: _ClassVar[int]
    code: int
    reason: str
    def __init__(self, code: _Optional[int] = ..., reason: _Optional[str] = ...) -> None: ...

class ValidationError(_message.Message):
    __slots__ = ("problems",)
    PROBLEMS_FIELD_NUMBER: _ClassVar[int]
    problems: _containers.RepeatedCompositeFieldContainer[Problem]
    def __init__(self, problems: _Optional[_Iterable[_Union[Problem, _Mapping]]] = ...) -> None: ...

class Problem(_message.Message):
    __slots__ = ("path", "message")
    PATH_FIELD_NUMBER: _ClassVar[int]
    MESSAGE_FIELD_NUMBER: _ClassVar[int]
    path: str
    message: str
    def __init__(self, path: _Optional[str] = ..., message: _Optional[str] = ...) -> None: ...
