import base64
import hashlib
import hmac
import re
import secrets

from .config import BBS_SYSOPS, LANGUAGES, PASSWORD_ITERATIONS, t
from .storage import USERS_FILE, load_json, now, save_json
from .ui import ask, ask_secret, banner, choice_menu, dialog_form, form_panel, line, pause

CALLSIGN_RE = re.compile(r"^[A-Z0-9][A-Z0-9/-]{2,15}$")
EMAIL_RE = re.compile(r"^[^@\s]+@[^@\s]+\.[^@\s]+$")
MAIDENHEAD_RE = re.compile(
    r"^[A-Ra-r]{2}([0-9]{2}([A-Xa-x]{2}([0-9]{2}([A-Xa-x]{2})?)?)?)?$"
)


def normalize_callsign(callsign: str) -> str:
    return callsign.strip().upper()


def normalize_user_callsigns(users: dict) -> tuple[dict, bool]:
    normalized = {}
    changed = False
    for callsign, profile in users.items():
        upper_callsign = normalize_callsign(str(callsign))
        if upper_callsign != callsign:
            changed = True
        if upper_callsign in normalized:
            changed = True
            existing = normalized[upper_callsign]
            if not existing.get("password_hash") and profile.get("password_hash"):
                normalized[upper_callsign] = profile
            elif existing.get("disabled") and not profile.get("disabled"):
                normalized[upper_callsign] = profile
        else:
            normalized[upper_callsign] = profile
    return normalized, changed


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
        prompt = f"{label} [{current}]" if current else label
        value = ask(prompt).strip()
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
        prompt = f"{t(lang, 'maidenhead')} [{current}]" if current else t(lang, "maidenhead")
        value = ask(prompt).strip()
        if not value:
            return current
        if MAIDENHEAD_RE.match(value):
            return normalize_locator(value)
        line(t(lang, "invalid_locator"))


def choose_language(lang: str = "en", current: str = "", required: bool = True) -> str:
    rows = [(str(idx), f"{name} ({code})") for idx, (code, name) in enumerate(LANGUAGES.items(), 1)]
    while True:
        current_value = ""
        if current in LANGUAGES:
            current_value = str(list(LANGUAGES).index(current) + 1)
        value = choice_menu(
            lang,
            t(lang, "select_language"),
            rows,
            allow_quit=not required,
            default_value=current_value,
        )
        if value in {"q", "quit", "exit"} and current and not required:
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
        password = ask_secret(t(lang, "new_password"))
        verify = ask_secret(t(lang, "verify_password"))
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


def profile_rows(lang: str, profile: dict) -> list[tuple[str, str]]:
    return [
        (t(lang, "full_name"), profile.get("full_name", "")),
        (t(lang, "email"), profile.get("email", "")),
        (t(lang, "maidenhead"), profile.get("maidenhead", "")),
        (t(lang, "language"), LANGUAGES.get(profile.get("language", ""), "")),
        (t(lang, "enable_aprs"), "true" if profile.get("enable_aprs") else "false"),
        (t(lang, "qth"), profile.get("qth", "")),
        (t(lang, "rig"), profile.get("rig", "")),
    ]


def collect_profile_form(
    callsign: str,
    profile: dict,
    lang: str,
    required: bool,
    title_key: str,
    include_password_change: bool = False,
) -> dict | None:
    title = f"{t(lang, title_key)} - {callsign}"
    profile.setdefault("full_name", "")
    profile.setdefault("email", "")
    profile.setdefault("maidenhead", "")
    profile.setdefault("language", lang)
    profile.setdefault("enable_aprs", False)
    profile.setdefault("qth", "")
    profile.setdefault("rig", "")

    fields = [
        {
            "name": "full_name",
            "label": t(lang, "full_name"),
            "value": profile.get("full_name", ""),
            "required": required,
            "limit": 100,
        },
        {
            "name": "email",
            "label": t(lang, "email"),
            "value": profile.get("email", ""),
            "required": required,
            "limit": 120,
            "validator": lambda value: bool(EMAIL_RE.match(value)),
            "invalid_key": "invalid_email",
        },
        {
            "name": "maidenhead",
            "label": t(lang, "maidenhead"),
            "value": profile.get("maidenhead", ""),
            "required": False,
            "limit": 10,
            "validator": lambda value: bool(MAIDENHEAD_RE.match(value)),
            "normalizer": normalize_locator,
            "invalid_key": "invalid_locator",
        },
        {
            "name": "language",
            "label": t(lang, "language"),
            "value": profile.get("language", lang),
            "required": required,
            "kind": "choice",
            "choices": [(code, f"{name} ({code})") for code, name in LANGUAGES.items()],
            "validator": lambda value: value in LANGUAGES,
            "invalid_key": "invalid_choice",
        },
        {
            "name": "enable_aprs",
            "label": t(lang, "enable_aprs"),
            "value": "true" if profile.get("enable_aprs") else "false",
            "required": False,
            "kind": "choice",
            "choices": [("false", "false"), ("true", "true")],
            "validator": lambda value: value in {"true", "false"},
            "invalid_key": "invalid_choice",
        },
        {
            "name": "qth",
            "label": t(lang, "qth"),
            "value": profile.get("qth", ""),
            "required": False,
            "limit": 80,
        },
        {
            "name": "rig",
            "label": t(lang, "rig"),
            "value": profile.get("rig", ""),
            "required": False,
            "limit": 100,
        },
    ]
    if include_password_change:
        fields.extend(
            [
                {
                    "name": "new_password",
                    "label": t(lang, "new_password"),
                    "value": "",
                    "required": False,
                    "secret": True,
                    "validator": lambda value: len(value) >= 8,
                    "invalid_key": "password_short",
                },
                {
                    "name": "verify_password",
                    "label": t(lang, "verify_password"),
                    "value": "",
                    "required": False,
                    "secret": True,
                    "matches": "new_password",
                    "mismatch_key": "password_mismatch",
                    "validator": lambda value: len(value) >= 8,
                    "invalid_key": "password_short",
                },
            ]
        )

    result = dialog_form(
        lang,
        title,
        fields,
        allow_cancel=not required,
    )
    if result is None:
        return None

    new_password = str(result.pop("new_password", ""))
    result.pop("verify_password", None)
    result["enable_aprs"] = str(result.get("enable_aprs", "false")) == "true"
    profile.update(result)
    if include_password_change and new_password:
        profile["password_hash"] = hash_password(new_password)
    lang = profile.get("language", lang)
    banner(lang)
    form_panel(f"{t(lang, title_key)} - {callsign}", profile_rows(lang, profile))
    return profile


def register_or_complete_profile(
    callsign: str, profile: dict, lang: str, force_password: bool
) -> dict:
    profile.setdefault("first_seen", now())
    profile.setdefault("qth", "")
    profile.setdefault("rig", "")
    profile["is_sysop"] = bool(profile.get("is_sysop") or callsign in BBS_SYSOPS)
    profile.setdefault("disabled", False)

    updated_profile = collect_profile_form(
        callsign,
        profile,
        lang,
        required=True,
        title_key="registration_form_title",
    )
    if updated_profile is None:
        raise SystemExit(1)
    profile = updated_profile

    if force_password or not profile.get("password_hash"):
        profile["password_hash"] = hash_password(ask_new_password(profile["language"]))

    profile["last_seen"] = now()
    return profile


def authenticate_user() -> tuple[str, dict]:
    users = load_json(USERS_FILE, {})
    users, users_changed = normalize_user_callsigns(users)
    if apply_configured_sysops(users):
        users_changed = True
    if users_changed:
        save_json(USERS_FILE, users)
    while True:
        banner(show_login_info=True)
        callsign = normalize_callsign(ask(t("en", "callsign_prompt")))
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
        password = ask_secret(t(lang, "password"))
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
