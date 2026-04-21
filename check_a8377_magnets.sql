SELECT CASE WHEN magnet LIKE 'magnet:%' THEN 'MAGNET' ELSE 'HTTP' END as type, COUNT(*) 
FROM resources 
WHERE entry_id='a8377e2a-8162-46b2-90f5-65d6450f5bcc' 
GROUP BY type;
