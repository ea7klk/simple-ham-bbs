#!/usr/bin/env python3
import base64
import datetime as dt
import getpass
import hashlib
import hmac
import json
import os
import re
import secrets
import sys
from pathlib import Path


def env_value(name: str, default: str) -> str:
    return os.environ.get(name) or default


DATA_DIR = Path(env_value("BBS_DATA_DIR", "/var/lib/bbs"))
USERS_FILE = DATA_DIR / "users.json"
MESSAGES_FILE = DATA_DIR / "messages.json"
BULLETINS_FILE = DATA_DIR / "bulletins.json"

BBS_NAME = env_value("BBS_NAME", "HAMNET RADIO BBS")
BBS_SYSOP = env_value("BBS_SYSOP", "Sysop")
BBS_SYSOPS = {
    item.strip().upper()
    for item in os.environ.get("BBS_SYSOPS", "").split(",")
    if item.strip()
}
BBS_LOCATION = env_value("BBS_LOCATION", "HamNet")
BBS_WELCOME_TOPIC = os.environ.get(
    "BBS_WELCOME_TOPIC",
    "Amateur radio notes, local nets, and packet-era experiments",
) or "Amateur radio notes, local nets, and packet-era experiments"

CALLSIGN_RE = re.compile(r"^[A-Z0-9][A-Z0-9/-]{2,15}$")
EMAIL_RE = re.compile(r"^[^@\s]+@[^@\s]+\.[^@\s]+$")
MAIDENHEAD_RE = re.compile(
    r"^[A-Ra-r]{2}([0-9]{2}([A-Xa-x]{2}([0-9]{2}([A-Xa-x]{2})?)?)?)?$"
)
PASSWORD_ITERATIONS = 200_000

LANGUAGES = {
    "en": "English",
    "es": "Espanol",
    "fr": "Francais",
    "de": "Deutsch",
}

APP_DIR = Path(__file__).resolve().parent
TRANSLATIONS_FILE = Path(
    env_value("BBS_TRANSLATIONS_FILE", str(APP_DIR / "translations.json"))
)


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


def now() -> str:
    return dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%d %H:%M UTC")


def clear() -> None:
    write("\033[2J\033[H")


def write(text: str = "") -> None:
    sys.stdout.write(text)
    sys.stdout.flush()


def line(text: str = "") -> None:
    write(text + "\r\n")


def ask(prompt: str) -> str:
    write(prompt)
    return sys.stdin.readline().strip()


def ask_secret(prompt: str) -> str:
    if sys.stdin.isatty():
        try:
            return getpass.getpass(prompt).strip()
        except (EOFError, KeyboardInterrupt):
            raise
    return ask(prompt).strip()


def pause(lang: str) -> None:
    ask(f"\r\n{t(lang, 'press_enter')}")


def load_json(path: Path, default):
    if not path.exists():
        return default
    try:
        return json.loads(path.read_text(encoding="utf-8"))
    except json.JSONDecodeError:
        return default


def save_json(path: Path, data) -> None:
    path.parent.mkdir(parents=True, exist_ok=True)
    tmp = path.with_name(f"{path.name}.{os.getpid()}.{secrets.token_hex(4)}.tmp")
    tmp.write_text(json.dumps(data, indent=2, sort_keys=True), encoding="utf-8")
    tmp.replace(path)


def hash_password(password: str) -> str:
    salt = secrets.token_bytes(16)
    digest = hashlib.pbkdf2_hmac(
        "sha256", password.encode("utf-8"), salt, PASSWORD_ITERATIONS
    )
    return "pbkdf2_sha256${}${}${}".format(
        PASSWORD_ITERATIONS,
        base64.b64encode(salt).decode("ascii"),
        base64.b64encode(digest).decode("ascii"),
    )


def verify_password(password: str, stored_hash: str) -> bool:
    try:
        algorithm, iterations, salt_b64, digest_b64 = stored_hash.split("$", 3)
        if algorithm != "pbkdf2_sha256":
            return False
        salt = base64.b64decode(salt_b64)
        expected = base64.b64decode(digest_b64)
        actual = hashlib.pbkdf2_hmac(
            "sha256", password.encode("utf-8"), salt, int(iterations)
        )
        return hmac.compare_digest(actual, expected)
    except (ValueError, TypeError):
        return False


def seed_data() -> None:
    DATA_DIR.mkdir(parents=True, exist_ok=True)
    if not BULLETINS_FILE.exists():
        save_json(
            BULLETINS_FILE,
            [
                {
                    "title": "Welcome",
                    "body": (
                        "This is a small HamNet-ready BBS for radio operators.\n"
                        "Use it for local notes, net announcements, and station contact info."
                    ),
                    "updated": now(),
                },
                {
                    "title": "Operating Notes",
                    "body": (
                        "Keep traffic courteous and relevant to amateur radio.\n"
                        "Do not post private keys, passwords, or third-party personal data."
                    ),
                    "updated": now(),
                },
            ],
        )
    if not MESSAGES_FILE.exists():
        save_json(MESSAGES_FILE, [])
    if not USERS_FILE.exists():
        save_json(USERS_FILE, {})


def banner(lang: str = "en", show_login_info: bool = False) -> None:
    clear()
    width = 72
    line("+" + "-" * (width - 2) + "+")
    line("|" + BBS_NAME.center(width - 2) + "|")
    subtitle = f"{BBS_LOCATION} - {BBS_WELCOME_TOPIC}"
    line("|" + subtitle.center(width - 2)[: width - 2] + "|")
    line("+" + "-" * (width - 2) + "+")
    line()
    if show_login_info:
        for code in ("en", "es", "fr", "de"):
            line(f"{LANGUAGES[code]}:")
            for item in t(code, "login_info"):
                line(f"- {item}")
            line()


def normalize_locator(locator: str) -> str:
    if not locator:
        return ""
    return locator[:2].upper() + locator[2:]


def profile_complete(profile: dict) -> bool:
    return all(
        [
            profile.get("full_name"),
            profile.get("email"),
            profile.get("language") in LANGUAGES,
            profile.get("password_hash"),
        ]
    )


def apply_configured_sysops(users: dict) -> bool:
    changed = False
    for callsign in BBS_SYSOPS:
        if callsign in users and not users[callsign].get("is_sysop"):
            users[callsign]["is_sysop"] = True
            changed = True
    return changed


def is_sysop(callsign: str, profile: dict) -> bool:
    return callsign in BBS_SYSOPS or bool(profile.get("is_sysop"))


def active_sysop_count(users: dict) -> int:
    return sum(
        1
        for callsign, profile in users.items()
        if is_sysop(callsign, profile) and not profile.get("disabled")
    )


def would_remove_last_sysop(users: dict, callsign: str) -> bool:
    profile = users.get(callsign, {})
    if not is_sysop(callsign, profile) or profile.get("disabled"):
        return False
    return active_sysop_count(users) <= 1


def ask_required(lang: str, label_key: str, current: str = "", required: bool = True) -> str:
    label = t(lang, label_key)
    while True:
        suffix = f" [{current}]" if current else ""
        value = ask(f"{label}{suffix}: ").strip()
        if value:
            return value
        if current and not required:
            return current
        if not required:
            return ""
        line(t(lang, "required"))


def ask_email(lang: str, current: str = "", required: bool = True) -> str:
    while True:
        value = ask_required(lang, "email", current, required)
        if not value and not required:
            return ""
        if EMAIL_RE.match(value):
            return value
        line(t(lang, "invalid_email"))


def ask_locator(lang: str, current: str = "") -> str:
    while True:
        suffix = f" [{current}]" if current else ""
        value = ask(f"{t(lang, 'maidenhead')}{suffix}: ").strip()
        if not value:
            return current
        if MAIDENHEAD_RE.match(value):
            return normalize_locator(value)
        line(t(lang, "invalid_locator"))


def choose_language(lang: str = "en", current: str = "", required: bool = True) -> str:
    while True:
        line(t(lang, "select_language"))
        for idx, code in enumerate(LANGUAGES, 1):
            marker = " *" if code == current else ""
            line(f"  {idx}. {LANGUAGES[code]} ({code}){marker}")
        value = ask(f"{t(lang, 'select')}: ").strip().lower()
        if not value and current and not required:
            return current
        if value in LANGUAGES:
            return value
        if value.isdigit():
            index = int(value) - 1
            codes = list(LANGUAGES)
            if 0 <= index < len(codes):
                return codes[index]
        line(t(lang, "invalid_choice"))


def ask_new_password(lang: str) -> str:
    while True:
        password = ask_secret(f"{t(lang, 'new_password')}: ")
        verify = ask_secret(f"{t(lang, 'verify_password')}: ")
        if not password:
            line(t(lang, "password_required"))
            continue
        if len(password) < 8:
            line(t(lang, "password_short"))
            continue
        if password != verify:
            line(t(lang, "password_mismatch"))
            continue
        return password


def register_or_complete_profile(
    callsign: str, profile: dict, lang: str, force_password: bool
) -> dict:
    profile.setdefault("first_seen", now())
    profile.setdefault("qth", "")
    profile.setdefault("rig", "")
    profile["is_sysop"] = bool(profile.get("is_sysop") or callsign in BBS_SYSOPS)
    profile.setdefault("disabled", False)

    profile["full_name"] = ask_required(
        lang, "full_name", profile.get("full_name", ""), required=True
    )[:100]
    profile["email"] = ask_email(lang, profile.get("email", ""), required=True)[:120]
    profile["maidenhead"] = ask_locator(lang, profile.get("maidenhead", ""))[:10]
    profile["language"] = choose_language(
        lang, profile.get("language", ""), required=True
    )

    if force_password or not profile.get("password_hash"):
        profile["password_hash"] = hash_password(ask_new_password(profile["language"]))

    profile["last_seen"] = now()
    return profile


def authenticate_user() -> tuple[str, dict]:
    users = load_json(USERS_FILE, {})
    if apply_configured_sysops(users):
        save_json(USERS_FILE, users)
    while True:
        banner(show_login_info=True)
        callsign = ask(t("en", "callsign_prompt")).upper()
        if CALLSIGN_RE.match(callsign):
            break
        line(t("en", "invalid_callsign"))
        pause("en")

    profile = users.get(callsign)
    if profile is None:
        line()
        line(t("en", "new_user"))
        profile = register_or_complete_profile(callsign, {}, "en", force_password=True)
        users[callsign] = profile
        save_json(USERS_FILE, users)
        return callsign, profile

    lang = profile.get("language", "en")
    profile["is_sysop"] = bool(profile.get("is_sysop") or callsign in BBS_SYSOPS)
    if profile.get("disabled"):
        line(t(lang, "user_disabled"))
        raise SystemExit(1)

    if not profile.get("password_hash"):
        line()
        line(t(lang, "complete_profile"))
        profile = register_or_complete_profile(callsign, profile, lang, force_password=True)
        users[callsign] = profile
        save_json(USERS_FILE, users)
        return callsign, profile

    for _ in range(3):
        password = ask_secret(f"{t(lang, 'password')}: ")
        if verify_password(password, profile["password_hash"]):
            if not profile_complete(profile):
                line()
                line(t(lang, "complete_profile"))
                profile = register_or_complete_profile(
                    callsign, profile, lang, force_password=False
                )
            profile["last_seen"] = now()
            users[callsign] = profile
            save_json(USERS_FILE, users)
            return callsign, profile
        line(t(lang, "wrong_password"))

    line(t(lang, "too_many_attempts"))
    raise SystemExit(1)


def menu(callsign: str, profile: dict, lang: str) -> str:
    banner(lang)
    role = f" [{t(lang, 'sysop_role')}]" if is_sysop(callsign, profile) else ""
    line(f"{t(lang, 'logged_in_as')} {callsign}{role}                         {t(lang, 'sysop')}: {BBS_SYSOP}")
    line()
    line(f"  1. {t(lang, 'menu_bulletins')}")
    line(f"  2. {t(lang, 'menu_messages')}")
    line(f"  3. {t(lang, 'menu_post')}")
    line(f"  4. {t(lang, 'menu_directory')}")
    line(f"  5. {t(lang, 'menu_profile')}")
    line(f"  6. {t(lang, 'menu_resources')}")
    line(f"  7. {t(lang, 'menu_aprs')}")
    line(f"  8. {t(lang, 'menu_about')}")
    if is_sysop(callsign, profile):
        line(f"  9. {t(lang, 'menu_sysop')}")
    line(f"  Q. {t(lang, 'menu_quit')}")
    line()
    return ask(f"{t(lang, 'select')}: ").lower()


def show_bulletins(lang: str) -> None:
    banner(lang)
    for item in load_json(BULLETINS_FILE, []):
        line(f"[{item.get('title', t(lang, 'untitled'))}]  {item.get('updated', '')}")
        line("-" * 72)
        for body_line in item.get("body", "").splitlines():
            line(body_line)
        line()
    pause(lang)


def show_messages(lang: str) -> None:
    banner(lang)
    messages = load_json(MESSAGES_FILE, [])
    if not messages:
        line(t(lang, "no_messages"))
        pause(lang)
        return

    for idx, message in enumerate(messages[-25:], 1):
        line(f"{idx:02d}. {message.get('subject', t(lang, 'untitled'))}")
        line(
            f"    {t(lang, 'from')} {message.get('from', 'UNKNOWN')} "
            f"{t(lang, 'at')} {message.get('created', '')}"
        )
        for body_line in message.get("body", "").splitlines():
            line(f"    {body_line}")
        line()
    pause(lang)


def post_message(callsign: str, lang: str) -> None:
    banner(lang)
    subject = ask(f"{t(lang, 'subject')}: ")[:80].strip()
    if not subject:
        line(t(lang, "cancelled"))
        pause(lang)
        return

    line(t(lang, "enter_message"))
    body_lines = []
    while True:
        text = sys.stdin.readline()
        if not text:
            break
        text = text.rstrip("\r\n")
        if text == ".":
            break
        body_lines.append(text[:120])

    if not body_lines:
        line(t(lang, "no_body"))
        pause(lang)
        return

    messages = load_json(MESSAGES_FILE, [])
    messages.append(
        {
            "from": callsign,
            "subject": subject,
            "body": "\n".join(body_lines),
            "created": now(),
        }
    )
    save_json(MESSAGES_FILE, messages[-500:])
    line(t(lang, "message_posted"))
    pause(lang)


def station_directory(lang: str) -> None:
    banner(lang)
    users = load_json(USERS_FILE, {})
    line(t(lang, "directory_header"))
    line("-" * 72)
    for callsign, profile in sorted(users.items()):
        if profile.get("disabled"):
            continue
        full_name = profile.get("full_name") or t(lang, "not_set")
        locator = profile.get("maidenhead") or t(lang, "not_set")
        user_lang = LANGUAGES.get(profile.get("language", ""), t(lang, "not_set"))
        line(
            f"{callsign:<16} {profile.get('last_seen', ''):<18} "
            f"{full_name} / {locator} / {user_lang}"
        )
    pause(lang)


def show_user_admin_list(lang: str, users: dict) -> None:
    line("CALLSIGN          STATUS        ROLE       NAME / EMAIL")
    line("-" * 72)
    for callsign, profile in sorted(users.items()):
        status = t(lang, "disabled") if profile.get("disabled") else t(lang, "enabled")
        role = t(lang, "sysop_role") if is_sysop(callsign, profile) else t(lang, "user_role")
        name = profile.get("full_name") or t(lang, "not_set")
        email = profile.get("email") or t(lang, "not_set")
        line(f"{callsign:<16} {status:<13} {role:<10} {name} / {email}")


def ask_existing_callsign(lang: str, users: dict, current_callsign: str) -> str:
    while True:
        callsign = ask(f"{t(lang, 'target_callsign')}: ").strip().upper()
        if callsign == current_callsign:
            line(t(lang, "cannot_manage_self"))
            return ""
        if callsign in users:
            return callsign
        line(t(lang, "user_not_found"))


def toggle_sysop_user(current_callsign: str, lang: str, users: dict) -> None:
    callsign = ask_existing_callsign(lang, users, current_callsign)
    if not callsign:
        pause(lang)
        return
    profile = users[callsign]
    if is_sysop(callsign, profile):
        if callsign in BBS_SYSOPS:
            line(t(lang, "cannot_demote_configured"))
        elif would_remove_last_sysop(users, callsign):
            line(t(lang, "cannot_remove_last_sysop"))
        else:
            profile["is_sysop"] = False
            line(t(lang, "user_updated"))
    else:
        profile["is_sysop"] = True
        profile["disabled"] = False
        line(t(lang, "user_updated"))
    save_json(USERS_FILE, users)
    pause(lang)


def toggle_disabled_user(current_callsign: str, lang: str, users: dict) -> None:
    callsign = ask_existing_callsign(lang, users, current_callsign)
    if not callsign:
        pause(lang)
        return
    profile = users[callsign]
    if not profile.get("disabled") and would_remove_last_sysop(users, callsign):
        line(t(lang, "cannot_remove_last_sysop"))
    else:
        profile["disabled"] = not profile.get("disabled")
        line(t(lang, "user_updated"))
    save_json(USERS_FILE, users)
    pause(lang)


def delete_user(current_callsign: str, lang: str, users: dict) -> None:
    callsign = ask_existing_callsign(lang, users, current_callsign)
    if not callsign:
        pause(lang)
        return
    if would_remove_last_sysop(users, callsign):
        line(t(lang, "cannot_remove_last_sysop"))
        pause(lang)
        return
    confirmation = ask(t(lang, "confirm_delete")).strip()
    if confirmation != "DELETE":
        line(t(lang, "delete_cancelled"))
        pause(lang)
        return
    users.pop(callsign, None)
    save_json(USERS_FILE, users)
    line(t(lang, "user_deleted"))
    pause(lang)


def sysop_administration(current_callsign: str, lang: str) -> None:
    while True:
        users = load_json(USERS_FILE, {})
        apply_configured_sysops(users)
        banner(lang)
        line(t(lang, "sysop_menu_title"))
        line("-" * 72)
        line(f"  1. {t(lang, 'sysop_list_users')}")
        line(f"  2. {t(lang, 'sysop_toggle_sysop')}")
        line(f"  3. {t(lang, 'sysop_toggle_disabled')}")
        line(f"  4. {t(lang, 'sysop_delete_user')}")
        line(f"  Q. {t(lang, 'menu_quit')}")
        line()
        choice = ask(f"{t(lang, 'select')}: ").lower()
        if choice == "1":
            banner(lang)
            show_user_admin_list(lang, users)
            pause(lang)
        elif choice == "2":
            banner(lang)
            show_user_admin_list(lang, users)
            line()
            toggle_sysop_user(current_callsign, lang, users)
        elif choice == "3":
            banner(lang)
            show_user_admin_list(lang, users)
            line()
            toggle_disabled_user(current_callsign, lang, users)
        elif choice == "4":
            banner(lang)
            show_user_admin_list(lang, users)
            line()
            delete_user(current_callsign, lang, users)
        elif choice in {"q", "quit", "exit"}:
            return


def change_profile(callsign: str, profile: dict, lang: str) -> tuple[dict, str]:
    banner(lang)
    users = load_json(USERS_FILE, {})
    profile = users.setdefault(callsign, profile)
    line(t(lang, "keep_blank"))

    profile["full_name"] = ask_required(
        lang, "full_name", profile.get("full_name", ""), required=False
    )[:100]
    profile["email"] = ask_email(lang, profile.get("email", ""), required=False)[:120]
    profile["maidenhead"] = ask_locator(lang, profile.get("maidenhead", ""))[:10]
    profile["language"] = choose_language(
        lang, profile.get("language", lang), required=False
    )
    profile["qth"] = ask_required(
        profile["language"], "qth", profile.get("qth", ""), required=False
    )[:80]
    profile["rig"] = ask_required(
        profile["language"], "rig", profile.get("rig", ""), required=False
    )[:100]

    answer = ask(t(profile["language"], "change_password")).strip().lower()
    if answer == t(profile["language"], "yes"):
        current = ask_secret(f"{t(profile['language'], 'current_password')}: ")
        if verify_password(current, profile.get("password_hash", "")):
            profile["password_hash"] = hash_password(ask_new_password(profile["language"]))
        else:
            line(t(profile["language"], "wrong_password"))

    profile["last_seen"] = now()
    users[callsign] = profile
    save_json(USERS_FILE, users)
    line(t(profile["language"], "profile_updated"))
    pause(profile["language"])
    return profile, profile["language"]


def radio_resources(lang: str) -> None:
    banner(lang)
    line(t(lang, "resources_title"))
    line("-" * 72)
    line(t(lang, "hamnet_address"))
    line(t(lang, "local_ssh"))
    line(t(lang, "hamnet_ssh"))
    line()
    line(t(lang, "ideas"))
    line("- Local net schedules")
    line("- Repeater status")
    line("- Packet, APRS, and mesh experiments")
    line("- Field-day planning notes")
    pause(lang)


def aprs_placeholder(lang: str) -> None:
    banner(lang)
    line(t(lang, "aprs_title"))
    line("-" * 72)
    line(t(lang, "aprs_not_enabled"))
    line()
    line(t(lang, "planned_extensions"))
    line("- APRS-IS server, callsign, and passcode settings from environment")
    line("- Inbox/outbox persisted under /var/lib/bbs/aprs")
    line("- Dry-run mode for local testing")
    line("- Menu actions for sending short APRS messages")
    pause(lang)


def about(lang: str) -> None:
    banner(lang)
    line(t(lang, "about_title"))
    line("-" * 72)
    line(t(lang, "about_transport"))
    line(t(lang, "about_hamnet"))
    line(t(lang, "about_storage"))
    line(t(lang, "about_graphics"))
    line()
    line(t(lang, "about_note"))
    pause(lang)


def main() -> int:
    seed_data()
    callsign, profile = authenticate_user()
    lang = profile.get("language", "en")

    while True:
        choice = menu(callsign, profile, lang)
        if choice == "1":
            show_bulletins(lang)
        elif choice == "2":
            show_messages(lang)
        elif choice == "3":
            post_message(callsign, lang)
        elif choice == "4":
            station_directory(lang)
        elif choice == "5":
            profile, lang = change_profile(callsign, profile, lang)
        elif choice == "6":
            radio_resources(lang)
        elif choice == "7":
            aprs_placeholder(lang)
        elif choice == "8":
            about(lang)
        elif choice == "9" and is_sysop(callsign, profile):
            sysop_administration(callsign, lang)
        elif choice in {"q", "quit", "exit"}:
            banner(lang)
            line(t(lang, "goodbye"))
            return 0


if __name__ == "__main__":
    try:
        raise SystemExit(main())
    except KeyboardInterrupt:
        line(f"\r\n{t('en', 'disconnected')}")
        raise SystemExit(0)
