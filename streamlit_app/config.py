API_BASE = "http://localhost:8000"

MOCKOON_QB = "http://localhost:3001/mock/qb"
MOCKOON_SLACK = "http://localhost:3001/mock/slack"
MOCKOON_LINEAR = "http://localhost:3001/mock/linear"


def get_headers(token: str = None) -> dict:
    headers = {"Content-Type": "application/json"}
    if token:
        headers["Authorization"] = f"Bearer {token}"
    return headers