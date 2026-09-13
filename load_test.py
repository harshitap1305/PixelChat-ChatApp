import asyncio
import aiohttp
import time

async def fetch(session, url):
    try:
        async with session.post(url, json={"client_name":"test","msg":"hello"}) as response:
            return await response.text()
    except Exception as e:
        return str(e)

async def main():
    async with aiohttp.ClientSession() as session:
        tasks = [fetch(session, "http://10.1.75.51:5269/message") for _ in range(500)]
        start = time.time()
        results = await asyncio.gather(*tasks)
        print(f"Time: {time.time()-start}")
        print(f"Success: {len([r for r in results if 'status' in r])}")
        print(f"Errors: {len([r for r in results if 'status' not in r])}")

asyncio.run(main())
