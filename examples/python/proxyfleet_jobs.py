"""Async job integration; one Fleet per process and one JobClient per session.

Management credentials stay on the management client. Closing a client keeps
its server assignment; call finish() explicitly when the logical session ends.
"""

from __future__ import annotations

import asyncio
from contextlib import asynccontextmanager
from dataclasses import dataclass
from typing import Callable
from urllib.parse import quote, unquote, urlsplit

import aiohttp


class JobUnavailable(RuntimeError):
    pass


@dataclass(frozen=True)
class Response:
    status: int
    headers: dict[str, str]
    body: bytes


class Fleet:
    def __init__(self, base_url: str, password: str):
        self.base_url = base_url.rstrip("/")
        self._password = password
        self._api = None
        self._limits: dict[str, asyncio.Semaphore] = {}

    async def __aenter__(self):
        self._api = aiohttp.ClientSession(timeout=aiohttp.ClientTimeout(total=30))
        try:
            auth = await self._call("/api/auth", {"password": self._password})
            if auth.get("token"):
                self._api.headers["Authorization"] = "Bearer " + auth["token"]
        except BaseException:
            await self._api.close()
            raise
        return self

    async def __aexit__(self, *exc):
        await self._api.close()

    async def _call(self, path, payload):
        async with self._api.post(self.base_url + path, json=payload, allow_redirects=False) as r:
            try:
                data = await r.json()
            except (ValueError, aiohttp.ContentTypeError) as exc:
                raise JobUnavailable(f"ProxyFleet API {r.status}: invalid JSON response") from exc
            if not isinstance(data, dict):
                raise JobUnavailable(f"ProxyFleet API {r.status}: expected a JSON object")
            if r.status >= 300:
                raise JobUnavailable(f"ProxyFleet API {r.status}: {data.get('error', 'failed')}")
            return data

    async def job(self, name: str, session: str = "") -> JobClient:
        path = "/api/jobs/" + quote(name, safe="")
        access = await self._call(path + "/acquire", {"session": session})
        JobClient.check_access(access)
        limit = self._limits.setdefault(name, asyncio.Semaphore(access["concurrency"]))
        return JobClient(self, path, access, limit)


class JobClient:
    def __init__(self, fleet, path, access, limit):
        self.fleet, self.path, self.access, self.limit = fleet, path, access, limit
        self._proxy_url, self._proxy_auth = access["proxy_url"], None
        proxy = urlsplit(self._proxy_url)
        if proxy.username is not None:
            # ProxyFleet compares UTF-8 credentials. aiohttp otherwise decodes
            # URL credentials into BasicAuth with its Latin-1 default encoding.
            self._proxy_auth = aiohttp.BasicAuth(
                unquote(proxy.username), unquote(proxy.password or ""), encoding="utf-8"
            )
            self._proxy_url = proxy._replace(netloc=proxy.netloc.rpartition("@")[2]).geturl()
        self.http = aiohttp.ClientSession(
            connector=aiohttp.TCPConnector(limit=access["concurrency"]),
            timeout=aiohttp.ClientTimeout(total=access["timeout_ms"] / 1000),
        )
        self._ended = False
        self._closing = False
        self._active = 0
        self._idle = asyncio.Event()
        self._idle.set()
        self._finish_lock = asyncio.Lock()
        self.last_feedback_error = None

    @staticmethod
    def check_access(access):
        if access["state"] != "ready" or not access.get("proxy_url"):
            raise JobUnavailable(f"Job {access['job']}: {access['state']}; recover the session explicitly")

    async def __aenter__(self):
        return self

    async def __aexit__(self, *exc):
        await self.close()

    async def close(self):
        self._closing = True
        await self._idle.wait()
        await self.http.close()

    async def finish(self):
        """Release the assignment only after all requests for this session end."""
        async with self._finish_lock:
            await self.close()
            if self.access["mode"] == "pinned" and not self._ended:
                await self.fleet._call(self.path + "/release", {"session": self.access["session"]})
            self._ended = True

    @asynccontextmanager
    async def _request_slot(self):
        async with self.limit:
            # Requests queued before close/finish must never recreate a lease.
            if self._closing or self.http.closed:
                raise JobUnavailable("Job client is closed")
            self._active += 1
            self._idle.clear()
            try:
                yield
            finally:
                self._active -= 1
                if self._active == 0:
                    self._idle.set()

    async def request(
        self, method: str, url: str, *,
        validate: Callable[[Response], bool] | None = None,
        report: bool = False, max_bytes: int = 32 << 20, **kwargs,
    ) -> Response:
        """Read the full response with pooled connections and a shared job limit.

        report=True is for representative requests to this job's benchmark
        target with selection=auto, not every login/redirect step. Manual jobs
        do not report target scores. There are no automatic retries.
        A 429 is returned to the caller to handle Retry-After and job scheduling.
        """
        if self._closing or self.http.closed:
            raise JobUnavailable("Job client is closed")
        if any(key in kwargs for key in ("proxy", "proxy_auth", "timeout")):
            raise ValueError("Job proxy and timeout come from ProxyFleet")
        if max_bytes < 1:
            raise ValueError("max_bytes must be positive")
        async with self._request_slot():
            if self.access["mode"] == "pinned":
                access = await self.fleet._call(
                    self.path + "/acquire", {"session": self.access["session"]}
                )
                self.check_access(access)
                if access["node"] != self.access["node"] or access["proxy_url"] != self.access["proxy_url"]:
                    raise JobUnavailable("Session assignment changed; close this client's cookies and connections")
            started = asyncio.get_running_loop().time()
            response = None
            success = False
            observed = False
            try:
                async with self.http.request(
                    method, url, proxy=self._proxy_url, proxy_auth=self._proxy_auth, **kwargs
                ) as r:
                    body = bytearray()
                    async for chunk in r.content.iter_chunked(64 << 10):
                        body.extend(chunk)
                        if len(body) > max_bytes:
                            raise ValueError("Response exceeds max_bytes")
                    response = Response(r.status, dict(r.headers), bytes(body))
                    success = (
                        validate(response) if validate else
                        r.status == self.access["expected_status"] and
                        self.access.get("body_contains", "").encode() in response.body
                    )
                    observed = True
                    return response
            finally:
                # Cancellation/local validation errors are not remote failure evidence.
                task = asyncio.current_task()
                if (report and observed and self.access["mode"] == "pinned"
                        and self.access.get("selection", "auto") == "auto" and not task.cancelling()):
                    duration = (asyncio.get_running_loop().time() - started) * 1000
                    try:
                        await self.fleet._call(self.path + "/feedback", {
                            "session": self.access["session"], "node": self.access["node"],
                            "success": success, "duration_ms": max(duration, 0.001),
                            "status_code": response.status,
                        })
                        self.last_feedback_error = None
                    except (JobUnavailable, aiohttp.ClientError, TimeoutError) as exc:
                        # A telemetry failure must not turn a completed POST into
                        # an apparent request failure that callers might replay.
                        self.last_feedback_error = exc
