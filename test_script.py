import asyncio
import valkey.asyncio as aiovalkey

async def test():
    v = aiovalkey.Redis()
    script = v.register_script("return 1")
    pipe = v.pipeline()
    await script(keys=['test'], args=['arg'], client=pipe)
    res = await pipe.execute()
    print("Result:", res)

asyncio.run(test())
