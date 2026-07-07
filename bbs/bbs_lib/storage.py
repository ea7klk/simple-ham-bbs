import datetime as dt
import json
import os
import re
import secrets
from pathlib import Path

from .config import BULLETINS_FILE, DATA_DIR, DEFAULT_BOARD_ID, MESSAGES_FILE, USERS_FILE

BOARD_ID_RE = re.compile(r"[^a-z0-9]+")


def now() -> str:
    return dt.datetime.now(dt.timezone.utc).strftime("%Y-%m-%d %H:%M UTC")


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


def board_id_from_name(name: str) -> str:
    board_id = BOARD_ID_RE.sub("-", name.lower()).strip("-")
    return board_id[:40] or DEFAULT_BOARD_ID


def default_board(messages: list | None = None) -> dict:
    return {
        "id": DEFAULT_BOARD_ID,
        "name": "General",
        "description": "General local messages",
        "created": now(),
        "messages": messages or [],
    }


def normalize_boards_data(data) -> dict:
    if isinstance(data, list):
        return {"boards": [default_board(data)]}
    if not isinstance(data, dict):
        return {"boards": [default_board()]}

    boards = data.get("boards")
    if not isinstance(boards, list):
        return {"boards": [default_board()]}

    normalized = []
    seen_ids = set()
    for board in boards:
        if not isinstance(board, dict):
            continue
        name = str(board.get("name") or board.get("id") or "General").strip()
        board_id = board_id_from_name(str(board.get("id") or name))
        if board_id in seen_ids:
            suffix = 2
            base = board_id
            while f"{base}-{suffix}" in seen_ids:
                suffix += 1
            board_id = f"{base}-{suffix}"
        seen_ids.add(board_id)
        messages = board.get("messages", [])
        normalized.append(
            {
                "id": board_id,
                "name": name[:60] or "General",
                "description": str(board.get("description") or "")[:120],
                "created": board.get("created") or now(),
                "messages": messages if isinstance(messages, list) else [],
            }
        )

    if not normalized:
        normalized.append(default_board())
    return {"boards": normalized}


def load_boards() -> dict:
    data = load_json(MESSAGES_FILE, {"boards": [default_board()]})
    boards_data = normalize_boards_data(data)
    if data != boards_data:
        save_json(MESSAGES_FILE, boards_data)
    return boards_data


def save_boards(boards_data: dict) -> None:
    save_json(MESSAGES_FILE, normalize_boards_data(boards_data))


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
    load_boards()
    if not USERS_FILE.exists():
        save_json(USERS_FILE, {})
