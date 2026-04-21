SELECT CASE WHEN magnet LIKE 'magnet:%' THEN 'MAGNET' ELSE 'HTTP' END as type, COUNT(*) 
FROM resources 
WHERE entry_id='f22dcb48-e8e3-4297-a332-289fd3ce5b91' 
GROUP BY type;
