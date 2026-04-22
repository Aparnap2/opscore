#!/usr/bin/env python
import asyncio
import asyncpg

async def test():
    conn = await asyncpg.connect('postgresql://iterateswarm:iterateswarm@localhost:5433/iterateswarm')

    # Test 1: Simple query
    print('=== Test 1: Connection & Simple Query ===')
    try:
        result = await conn.fetchval('SELECT 1 as test')
        print(f'PASS: SELECT 1 returned {result}')
    except Exception as e:
        print(f'FAIL: {e}')

    # Test 2: pgvector extension
    print('\n=== Test 2: pgvector Extension ===')
    try:
        row = await conn.fetchrow("SELECT extname, extversion FROM pg_extension WHERE extname = 'vector'")
        if row:
            print(f'PASS: vector {row["extversion"]}')
        else:
            print('FAIL: vector extension not found')
    except Exception as e:
        print(f'FAIL: {e}')

    # Test 3: Tables exist
    print('\n=== Test 3: Tables ===')
    try:
        rows = await conn.fetch("SELECT tablename FROM pg_tables WHERE schemaname = 'public'")
        tables = {r['tablename'] for r in rows}
        expected = ['compliance_chunks', 'checkpoints', 'arq_results']
        for t in expected:
            if t in tables:
                print(f'PASS: {t} exists')
            else:
                print(f'FAIL: {t} not found')
    except Exception as e:
        print(f'FAIL: {e}')

    await conn.close()

asyncio.run(test())
