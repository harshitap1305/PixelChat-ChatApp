import asyncio
import aiohttp
import time

async def fetch(session, url):
    try:
        async with session.get(url) as response:
            return await response.read()
    except Exception as e:
        return str(e).encode()

async def main():
    async with aiohttp.ClientSession() as session:
        tasks = [fetch(session, "http://10.1.75.51:5269/feed?limit=50000") for _ in range(200)]
        start = time.time()
        results = await asyncio.gather(*tasks)
        print(f"Time: {time.time()-start}")
        print(f"Success: {len([r for r in results if b'messages' in r])}")
        print(f"Errors: {len([r for r in results if b'messages' not in r])}")

asyncio.run(main())
