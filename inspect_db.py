import sqlite3, json, os, textwrap

p = r'D:\Work\Project\ToolProject\Atom2Api\data\atom2api.db'
conn = sqlite3.connect(f'file:{p}?mode=ro', uri=True)
conn.row_factory = sqlite3.Row
c = conn.cursor()

print("=" * 60)
print("1. ACCOUNTS")
print("=" * 60)

c.execute("SELECT id, data FROM atom2api_accounts ORDER BY sort_order")
for row in c.fetchall():
    data = json.loads(row['data'])
    acct_id = row['id']
    last_sync = data.get('last_sync_at') or data.get('LastSync')
    plan = data.get('plan', {})
    plan_name = plan.get('codingplan_free', {}).get('plan_name', 'N/A')
    models = data.get('models', [])
    display_names = []
    for m in models:
        if isinstance(m, dict):
            display_names.append(m.get('display_model_name') or m.get('DisplayName') or m.get('Model') or str(m))
        else:
            display_names.append(str(m))
    print(f"\nAccount id: {acct_id}")
    print(f"  plan_name: {plan_name}")
    print(f"  last_sync: {last_sync}")
    print(f"  model_display_names: {display_names}")
    has_qwen38 = any(
        (m.get('Model') if isinstance(m, dict) else str(m)) == 'qwen3.8-27b'
        for m in models
    )
    print(f"  qwen3.8-27b present in account.models: {has_qwen38}")

print("\n" + "=" * 60)
print("2. MODEL SETTINGS")
print("=" * 60)

c.execute("SELECT upstream, data FROM atom2api_model_settings ORDER BY upstream")
rows = c.fetchall()
if not rows:
    print("  (no model_settings rows)")
for row in rows:
    data = json.loads(row['data'])
    print(f"\nupstream: {row['upstream']}")
    print(json.dumps(data, indent=2, ensure_ascii=False))

print("\n" + "=" * 60)
print("3. RECENT USAGE RECORDS (last 20)")
print("=" * 60)

c.execute("""
    SELECT seq, id, timestamp, data FROM atom2api_usage_records
    ORDER BY seq DESC LIMIT 20
""")
rows = c.fetchall()
if not rows:
    print("  (no usage records)")
for row in rows:
    data = json.loads(row['data'])
    print(f"\nseq={row['seq']} id={row['id']} ts={row['timestamp']}")
    snippet = data.get('response_body')
    if isinstance(snippet, str):
        snippet = textwrap.shorten(snippet, width=300)
    print(json.dumps({
        'model': data.get('model'),
        'status': data.get('status'),
        'error': data.get('error'),
        'path': data.get('path'),
        'upstream_model': data.get('upstream_model'),
        'response_body': snippet,
        'request_id': data.get('request_id'),
    }, indent=2, ensure_ascii=False))

print("\n" + "=" * 60)
print("4. FILTERED USAGE RECORDS")
print("=" * 60)

filters = {
    'qwen3.8-27b': lambda d: 'qwen3.8-27b' in json.dumps(d),
    'Qwen': lambda d: 'Qwen' in json.dumps(d),
    'request_id req_qyLKrpKXmxWS': lambda d: d.get('request_id') == 'req_qyLKrpKXmxWS',
    'status 400': lambda d: str(d.get('status')) == '400',
}

for label, pred in filters.items():
    print(f"\n--- filter: {label} ---")
    c.execute("SELECT seq, id, timestamp, data FROM atom2api_usage_records ORDER BY seq DESC")
    found = 0
    for row in c.fetchall():
        data = json.loads(row['data'])
        if pred(data):
            found += 1
            print(f"\nseq={row['seq']} id={row['id']} ts={row['timestamp']}")
            snippet = data.get('response_body')
            if isinstance(snippet, str):
                snippet = textwrap.shorten(snippet, width=300)
            print(json.dumps({
                'model': data.get('model'),
                'status': data.get('status'),
                'error': data.get('error'),
                'path': data.get('path'),
                'upstream_model': data.get('upstream_model'),
                'response_body': snippet,
                'request_id': data.get('request_id'),
            }, indent=2, ensure_ascii=False))
    if found == 0:
        print("  (none found)")

conn.close()
