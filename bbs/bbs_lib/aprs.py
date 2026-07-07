import re
import socket

from .config import (
    APRS_APP_NAME,
    APRS_APP_VERSION,
    APRS_IS_PORT,
    APRS_IS_SERVER,
    APRS_SENT_FILE,
)
from .storage import load_json, now, save_json

APRS_CALLSIGN_RE = re.compile(r"^[A-Z0-9]{1,6}(-[0-9]{1,2})?$")
APRS_MESSAGE_LIMIT = 67
SENT_HISTORY_LIMIT = 10


def aprs_base_callsign(callsign: str) -> str:
    return callsign.strip().upper().split("-", 1)[0]


def normalize_aprs_callsign(callsign: str) -> str:
    return callsign.strip().upper()


def valid_aprs_callsign(callsign: str) -> bool:
    return bool(APRS_CALLSIGN_RE.match(normalize_aprs_callsign(callsign)))


def aprs_is_passcode(callsign: str) -> int:
    base = aprs_base_callsign(callsign)
    code = 0x73E2
    for index, char in enumerate(base):
        if index % 2 == 0:
            code ^= ord(char) << 8
        else:
            code ^= ord(char)
    return code & 0x7FFF


def aprs_message_packet(source: str, destination: str, text: str) -> str:
    source = aprs_base_callsign(source)
    destination = normalize_aprs_callsign(destination)
    body = text.replace("\r", " ").replace("\n", " ")[:APRS_MESSAGE_LIMIT]
    body = body.encode("ascii", "replace").decode("ascii")
    return f"{source}>APRS,TCPIP*::{destination:<9}:{body}"


def aprs_login_line(callsign: str) -> str:
    source = aprs_base_callsign(callsign)
    passcode = aprs_is_passcode(source)
    return f"user {source} pass {passcode} vers {APRS_APP_NAME} {APRS_APP_VERSION}"


def send_aprs_message(source: str, destination: str, text: str) -> tuple[bool, str]:
    if not valid_aprs_callsign(source):
        return False, "invalid_source"
    if not valid_aprs_callsign(destination):
        return False, "invalid_destination"
    packet = aprs_message_packet(source, destination, text)
    try:
        with socket.create_connection((APRS_IS_SERVER, APRS_IS_PORT), timeout=10) as sock:
            sock.settimeout(10)
            sock.sendall(f"{aprs_login_line(source)}\r\n".encode("ascii"))
            try:
                sock.recv(1024)
            except socket.timeout:
                pass
            sock.sendall(f"{packet}\r\n".encode("ascii"))
        return True, packet
    except (OSError, UnicodeEncodeError) as exc:
        return False, str(exc)


def load_sent_messages() -> dict:
    data = load_json(APRS_SENT_FILE, {})
    return data if isinstance(data, dict) else {}


def trim_sent_messages(callsign: str) -> list[dict]:
    sent = load_sent_messages()
    key = callsign.upper()
    messages = sent.get(key, [])
    if not isinstance(messages, list):
        messages = []
    messages = messages[-SENT_HISTORY_LIMIT:]
    sent[key] = messages
    save_json(APRS_SENT_FILE, sent)
    return messages


def add_sent_message(callsign: str, destination: str, text: str, status: str) -> list[dict]:
    sent = load_sent_messages()
    key = callsign.upper()
    messages = sent.get(key, [])
    if not isinstance(messages, list):
        messages = []
    messages.append(
        {
            "at": now(),
            "to": normalize_aprs_callsign(destination),
            "text": text,
            "status": status,
        }
    )
    messages = messages[-SENT_HISTORY_LIMIT:]
    sent[key] = messages
    save_json(APRS_SENT_FILE, sent)
    return messages
