"""The h3-v2 scenario's HTTP/3 server (see serveAioquic in aioquic.go).

aioquic (pip install aioquic) offering QUIC versions 1 and 2: a client
whose version_information lists version 2 is upgraded to it compatibly
(RFC 9368), as aioquic, picoquic and others do on the Internet. Every
request is answered 200 "ok". It binds 127.0.0.1 on a free port, prints the
port, then serves until killed, logging every TLS secret (NSS key log
format, flushed per line) so the capture can open both directions.
"""

import argparse
import asyncio

from aioquic.asyncio import QuicConnectionProtocol, serve
from aioquic.h3.connection import H3_ALPN, H3Connection
from aioquic.h3.events import DataReceived, HeadersReceived
from aioquic.quic.configuration import QuicConfiguration
from aioquic.quic.events import ProtocolNegotiated
from aioquic.quic.packet import QuicProtocolVersion


class Handler(QuicConnectionProtocol):
    def __init__(self, *args, **kwargs):
        super().__init__(*args, **kwargs)
        self._h3 = None

    def quic_event_received(self, event):
        if isinstance(event, ProtocolNegotiated):
            self._h3 = H3Connection(self._quic)
        if self._h3 is None:
            return
        for e in self._h3.handle_event(event):
            if isinstance(e, (HeadersReceived, DataReceived)) and e.stream_ended:
                self._answer(e.stream_id)

    def _answer(self, stream_id):
        body = b"ok\n"
        self._h3.send_headers(
            stream_id,
            [
                (b":status", b"200"),
                (b"content-type", b"text/plain"),
                (b"content-length", str(len(body)).encode()),
                (b"cache-control", b"no-store"),
            ],
        )
        self._h3.send_data(stream_id, body, end_stream=True)
        self.transmit()


async def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--certificate", required=True)
    parser.add_argument("--private-key", required=True)
    parser.add_argument("--secrets-log", required=True)
    args = parser.parse_args()
    config = QuicConfiguration(
        is_client=False,
        alpn_protocols=H3_ALPN,
        supported_versions=[
            QuicProtocolVersion.VERSION_1,
            QuicProtocolVersion.VERSION_2,
        ],
        idle_timeout=60.0,
        secrets_log_file=open(args.secrets_log, "a"),
    )
    config.load_cert_chain(args.certificate, args.private_key)
    tickets = {}
    server = await serve(
        "127.0.0.1",
        0,
        configuration=config,
        create_protocol=Handler,
        session_ticket_fetcher=lambda label: tickets.pop(label, None),
        session_ticket_handler=lambda ticket: tickets.__setitem__(ticket.ticket, ticket),
    )
    print(server._transport.get_extra_info("sockname")[1], flush=True)
    await asyncio.Future()


if __name__ == "__main__":
    asyncio.run(main())
