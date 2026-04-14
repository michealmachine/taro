#!/bin/sh
docker exec taro sh -c "cat > /tmp/q.sql << 'ENDSQL'
SELECT substr(title,1,60), resolution, seeders FROM resources WHERE entry_id=(SELECT id FROM entries WHERE title='Shingeki no Kyojin' LIMIT 1) AND eligible=1 ORDER BY seeders DESC LIMIT 10;
ENDSQL
"
docker cp taro:/tmp/q.sql /tmp/q.sql
docker run --rm -v ~/taro/data:/data -v /tmp/q.sql:/q.sql alpine sh -c 'apk add -q sqlite && sqlite3 /data/taro.db < /q.sql'
