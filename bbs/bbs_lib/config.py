import json
import os
from pathlib import Path


def env_value(name: str, default: str) -> str:
    return os.environ.get(name) or default


APP_DIR = Path(__file__).resolve().parent.parent
DATA_DIR = Path(env_value("BBS_DATA_DIR", "/var/lib/bbs"))
USERS_FILE = DATA_DIR / "users.json"
MESSAGES_FILE = DATA_DIR / "messages.json"
BULLETINS_FILE = DATA_DIR / "bulletins.json"
APRS_DIR = DATA_DIR / "aprs"
APRS_SENT_FILE = APRS_DIR / "sent.json"
TRANSLATIONS_FILE = Path(
    env_value("BBS_TRANSLATIONS_FILE", str(APP_DIR / "translations.json"))
)

BBS_NAME = env_value("BBS_NAME", "HAMNET RADIO BBS")
BBS_SYSOP = env_value("BBS_SYSOP", "Sysop")
BBS_SYSOPS = {
    item.strip().upper()
    for item in os.environ.get("BBS_SYSOPS", "").split(",")
    if item.strip()
}
BBS_LOCATION = env_value("BBS_LOCATION", "HamNet")
BBS_WELCOME_TOPIC = (
    os.environ.get(
        "BBS_WELCOME_TOPIC",
        "Amateur radio notes, local nets, and packet-era experiments",
    )
    or "Amateur radio notes, local nets, and packet-era experiments"
)
APRS_IS_SERVER = env_value("APRS_IS_SERVER", "rotate.aprs2.net")
APRS_IS_PORT = int(env_value("APRS_IS_PORT", "14580"))
APRS_APP_NAME = env_value("APRS_APP_NAME", "HamNetBBS")
APRS_APP_VERSION = env_value("APRS_APP_VERSION", "0.1")

PASSWORD_ITERATIONS = 200_000
DEFAULT_BOARD_ID = "general"
PAGE_SIZE = int(env_value("BBS_PAGE_SIZE", "10"))

LANGUAGES = {
    "en": "English",
    "es": "Espanol",
    "fr": "Francais",
    "de": "Deutsch",
}


def load_translations() -> dict:
    try:
        return json.loads(TRANSLATIONS_FILE.read_text(encoding="utf-8"))
    except FileNotFoundError:
        raise SystemExit(f"Missing translations file: {TRANSLATIONS_FILE}")
    except json.JSONDecodeError as exc:
        raise SystemExit(f"Invalid translations file {TRANSLATIONS_FILE}: {exc}")


TEXT = load_translations()


def t(lang: str, key: str):
    return TEXT.get(lang, TEXT["en"]).get(key, TEXT["en"][key])
