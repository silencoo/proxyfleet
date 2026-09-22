import asyncio
import base64
import unittest
from urllib.parse import quote

from aiohttp import ClientHttpProxyError, web

from proxyfleet_jobs import Fleet, JobUnavailable


class JobClientTests(unittest.IsolatedAsyncioTestCase):
    async def asyncSetUp(self):
        self.states, self.feedback, self.releases = {}, [], []
        self.active = self.peak = self.requests = 0
        self.fail_feedback = False
        self.feedback_body = None
        self.acquires = []
        self.acquire_entered = self.acquire_continue = None
        self.selection = "auto"
        self.proxy_headers = []
        app = web.Application()
        app.router.add_post("/api/auth", self.auth)
        app.router.add_post("/api/jobs/{job}/{action}", self.api)
        app.router.add_route("*", "/{tail:.*}", self.proxy)
        self.runner = web.AppRunner(app)
        await self.runner.setup()
        site = web.TCPSite(self.runner, "127.0.0.1", 0)
        await site.start()
        self.base = f"http://127.0.0.1:{site._server.sockets[0].getsockname()[1]}"
        self.proxy_url = self.base

    async def asyncTearDown(self):
        await self.runner.cleanup()

    async def auth(self, request):
        self.assertEqual((await request.json())["password"], "test-password")
        return web.json_response({"token": "test-admin-token"})

    async def api(self, request):
        self.assertEqual(request.headers.get("Authorization"), "Bearer test-admin-token")
        data = await request.json()
        action = request.match_info["action"]
        job = request.match_info["job"]
        if action == "feedback":
            self.feedback.append(data)
            if self.feedback_body is not None:
                return web.Response(text=self.feedback_body, content_type="application/json")
            if self.fail_feedback:
                return web.json_response({"error": "temporarily unavailable"}, status=503)
        elif action == "release":
            self.releases.append(data["session"])
        else:
            if self.acquire_entered is not None:
                self.acquire_entered.set()
                await self.acquire_continue.wait()
            self.acquires.append(data.get("session", ""))
            return web.json_response({
                "job": job, "mode": "pinned" if job == "accounts" else "pooled",
                "selection": self.selection,
                "state": self.states.get(data.get("session"), "ready"),
                "proxy_url": self.proxy_url, "node": "stable-node", "session": data.get("session", ""),
                "concurrency": 2, "timeout_ms": 100, "expected_status": 200,
            })
        return web.json_response({"ok": True})

    async def proxy(self, request):
        self.proxy_headers.append(dict(request.headers))
        self.requests += 1
        self.active += 1
        self.peak = max(self.peak, self.active)
        try:
            await asyncio.sleep(.3 if "slow" in request.path else .01)
            response = web.Response(text=request.headers.get("Cookie", "no-cookie"))
            if "cookie" in request.path:
                response.set_cookie("account", "a")
            return response
        finally:
            self.active -= 1

    async def test_pinned_cookies_revalidation_and_explicit_release(self):
        async with Fleet(self.base, "test-password") as fleet:
            async with await fleet.job("accounts", "a") as a, await fleet.job("accounts", "b") as b:
                await a.request("GET", "http://example.test/cookie")
                same = await a.request("GET", "http://example.test/page", report=True)
                other = await b.request("GET", "http://example.test/page")
                self.assertIn(b"account=a", same.body)
                self.assertEqual(other.body, b"no-cookie")
                self.assertEqual(self.feedback[0]["node"], "stable-node")
                self.assertTrue(self.feedback[0]["success"])
                count = self.requests
                self.states["a"] = "paused"
                with self.assertRaises(JobUnavailable):
                    await a.request("GET", "http://example.test/page")
                self.assertEqual(self.requests, count)
                await a.finish()
            self.assertEqual(self.releases, ["a"])
            self.assertTrue(all("Authorization" not in headers for headers in self.proxy_headers))

    async def test_shared_concurrency_and_total_timeout(self):
        async with Fleet(self.base, "test-password") as fleet:
            async with await fleet.job("catalog") as a, await fleet.job("catalog") as b:
                await asyncio.gather(*(client.request("GET", "http://example.test/page") for client in [a, b] * 5))
                self.assertLessEqual(self.peak, 2)
                with self.assertRaises(TimeoutError):
                    await a.request("GET", "http://example.test/slow")

    async def test_proxy_credentials_use_utf8_and_preserve_reserved_characters(self):
        async with Fleet(self.base, "test-password") as fleet:
            for username, password in (
                ("worker-session-a", "p@ss?/secret:+%20"),
                ("worker-session-a", "sécret"),
                ("任务-session-a", "p@ss?/秘密"),
            ):
                with self.subTest(username=username, password=password):
                    self.proxy_url = (
                        "http://" + quote(username, safe="") + ":" + quote(password, safe="")
                        + "@" + self.base.removeprefix("http://")
                    )
                    async with await fleet.job("accounts", "a") as client:
                        response = await client.request("GET", "http://example.test/page")
                        self.assertEqual(response.status, 200)
                        expected = "Basic " + base64.b64encode(f"{username}:{password}".encode("utf-8")).decode("ascii")
                        self.assertEqual(self.proxy_headers[-1].get("Proxy-Authorization"), expected)
                        self.assertNotIn("Authorization", self.proxy_headers[-1])

    async def test_https_connect_uses_utf8_proxy_credentials_without_target_auth(self):
        received = asyncio.Queue()

        async def proxy(reader, writer):
            try:
                received.put_nowait(await reader.readuntil(b"\r\n\r\n"))
                # Inspect CONNECT without contacting a remote TLS server.
                writer.write(b"HTTP/1.1 502 Bad Gateway\r\nContent-Length: 0\r\nConnection: close\r\n\r\n")
                await writer.drain()
            except asyncio.IncompleteReadError:
                pass
            finally:
                writer.close()
                await writer.wait_closed()

        server = await asyncio.start_server(proxy, "127.0.0.1", 0)
        async with server:
            credentials = "worker-session-a:密码@:/+%20"
            username, password = credentials.split(":", 1)
            port = server.sockets[0].getsockname()[1]
            self.proxy_url = f"http://{quote(username, safe='')}:{quote(password, safe='')}@127.0.0.1:{port}"
            async with Fleet(self.base, "test-password") as fleet:
                async with await fleet.job("accounts", "a") as client:
                    with self.assertRaises(ClientHttpProxyError) as raised:
                        await client.request("GET", "https://example.test/page", headers={"Authorization": "Bearer target-token"})
                    self.assertEqual(raised.exception.status, 502)
            lines = (await asyncio.wait_for(received.get(), 1)).split(b"\r\n")
            self.assertEqual(lines[0], b"CONNECT example.test:443 HTTP/1.1")
            headers = dict((key.lower(), value.strip()) for key, value in (line.split(b":", 1) for line in lines[1:] if line))
            self.assertEqual(headers[b"proxy-authorization"], b"Basic " + base64.b64encode(credentials.encode("utf-8")))
            self.assertNotIn(b"authorization", headers)

    async def test_feedback_failure_does_not_hide_completed_request(self):
        self.fail_feedback = True
        async with Fleet(self.base, "test-password") as fleet:
            async with await fleet.job("accounts", "a") as a:
                response = await a.request("POST", "http://example.test/page", report=True)
                self.assertEqual(response.status, 200)
                self.assertEqual(self.requests, 1)
                self.assertIsInstance(a.last_feedback_error, JobUnavailable)

    async def test_manual_session_does_not_submit_target_feedback(self):
        self.selection = "manual"
        async with Fleet(self.base, "test-password") as fleet:
            async with await fleet.job("accounts", "a") as a:
                response = await a.request("GET", "http://example.test/page", report=True)
                self.assertEqual(response.status, 200)
                self.assertEqual(self.feedback, [])
                self.assertIsNone(a.last_feedback_error)

    async def test_invalid_feedback_response_does_not_hide_completed_post(self):
        async with Fleet(self.base, "test-password") as fleet:
            async with await fleet.job("accounts", "a") as a:
                for body in ("{broken", "[]"):
                    with self.subTest(body=body):
                        self.feedback_body = body
                        before = self.requests
                        response = await a.request("POST", "http://example.test/page", report=True)
                        self.assertEqual(response.status, 200)
                        self.assertEqual(self.requests, before + 1)
                        self.assertIsInstance(a.last_feedback_error, JobUnavailable)

    async def test_finish_rejects_queued_requests_without_reacquiring(self):
        async with Fleet(self.base, "test-password") as fleet:
            async with await fleet.job("accounts", "a") as a:
                await a.limit.acquire()
                await a.limit.acquire()
                queued = asyncio.create_task(a.request("GET", "http://example.test/page"))
                await asyncio.sleep(0)
                try:
                    await a.finish()
                finally:
                    a.limit.release()
                    a.limit.release()
                with self.assertRaises(JobUnavailable):
                    await queued
                self.assertEqual(self.acquires, ["a"])
                self.assertEqual(self.releases, ["a"])
                self.assertEqual(self.requests, 0)

    async def test_finish_waits_for_inflight_revalidation_and_releases_once(self):
        async with Fleet(self.base, "test-password") as fleet:
            async with await fleet.job("accounts", "a") as a:
                self.acquire_entered, self.acquire_continue = asyncio.Event(), asyncio.Event()
                pending = asyncio.create_task(a.request("GET", "http://example.test/page"))
                await asyncio.wait_for(self.acquire_entered.wait(), 1)
                finishes = [asyncio.create_task(a.finish()) for _ in range(2)]
                try:
                    await asyncio.sleep(.02)
                    self.assertEqual(self.releases, [])
                    self.assertTrue(all(not task.done() for task in finishes))
                finally:
                    self.acquire_continue.set()
                    results = await asyncio.gather(pending, *finishes, return_exceptions=True)
                self.assertEqual(results[0].status, 200)
                self.assertEqual(results[1:], [None, None])
                self.assertEqual(self.releases, ["a"])


if __name__ == "__main__":
    unittest.main()
