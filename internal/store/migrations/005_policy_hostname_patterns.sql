-- Preserve deployed rule IDs, order and restrictions while changing only the
-- stored hostname representation. Invalidate browser edits using the old shape.
UPDATE settings
SET value=json_set(
 value,
 '$.revision',coalesce(json_extract(value,'$.revision'),0)+1,
 '$.rules',json((
   SELECT json_group_array(json(rule))
   FROM (
     SELECT CASE WHEN json_type(value,'$.hostname_regex')='text'
       THEN json_remove(json_set(value,'$.hostname_regexes',json_array(json_extract(value,'$.hostname_regex'))),'$.hostname_regex')
       ELSE value END AS rule
     FROM json_each(settings.value,'$.rules')
     ORDER BY key
   )
 ))
)
WHERE name='exposure_policy' AND secret=0
AND EXISTS (
 SELECT 1 FROM json_each(settings.value,'$.rules')
 WHERE json_type(value,'$.hostname_regex')='text'
);
