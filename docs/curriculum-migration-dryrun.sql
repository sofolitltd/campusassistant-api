-- Run read-only: PGOPTIONS='-c default_transaction_read_only=on' psql "$DATABASE_URL" -X -q -f docs/curriculum-migration-dryrun.sql
-- Curriculum-model migration dry run: CU Psychology. Read-only.
\set dept '''cdc0f543-2bef-4c8d-9fdf-86d4c017ea84'''
\pset footer off

\echo '== 1. Inventory'
select (select count(*) from levels where department_id=:dept and deleted_at is null) levels,
       (select count(*) from courses where department_id=:dept and deleted_at is null) courses,
       (select count(*) from chapters where department_id=:dept and deleted_at is null) chapters,
       (select count(*) from batches where department_id=:dept and deleted_at is null) batches,
       (select count(*) from resources where department_id=:dept and deleted_at is null) resources,
       (select count(*) from resources where department_id=:dept and deleted_at is not null) soft_deleted_resources;

\echo '== 2. Resources by type/status (with lesson_no>0)'
select type, status, count(*) n, count(*) filter (where lesson_no>0) with_lesson
from resources where department_id=:dept and deleted_at is null group by 1,2 order by 1,2;

\echo '== 3. Resource -> course match (by course_code within dept)'
select
 count(*) filter (where (select count(*) from courses c where c.course_code=r.course_code and c.department_id=r.department_id and c.deleted_at is null)=1) one_course,
 count(*) filter (where (select count(*) from courses c where c.course_code=r.course_code and c.department_id=r.department_id and c.deleted_at is null)>1) multi_course,
 count(*) filter (where (select count(*) from courses c where c.course_code=r.course_code and c.department_id=r.department_id and c.deleted_at is null)=0) no_course,
 count(*) filter (where coalesce(r.course_code,'')='') empty_code
from resources r where department_id=:dept and deleted_at is null;

\echo '== 3b. Unmatched course codes'
select r.course_code, r.type, count(*) from resources r
where department_id=:dept and deleted_at is null
  and not exists (select 1 from courses c where c.course_code=r.course_code and c.department_id=r.department_id and c.deleted_at is null)
group by 1,2 order by 3 desc;

\echo '== 4. Resource -> chapter match (lesson_no>0)'
select type,
 count(*) filter (where m=1) one_chapter,
 count(*) filter (where m>1) multi_chapter,
 count(*) filter (where m=0) no_chapter
from (select r.type, (select count(*) from chapters ch where ch.course_code=r.course_code and ch.department_id=r.department_id and ch.chapter_no=r.lesson_no and ch.deleted_at is null) m
      from resources r where department_id=:dept and deleted_at is null and lesson_no>0) x group by 1 order by 1;

\echo '== 4b. Notes/videos with no lesson_no (would land at course level)'
select type, count(*) from resources where department_id=:dept and deleted_at is null and coalesce(lesson_no,0)=0 and type in ('note','video') group by 1;

\echo '== 4c. lesson_no with no chapter: detail'
select r.course_code, r.lesson_no, r.type, count(*) from resources r
where department_id=:dept and deleted_at is null and lesson_no>0
 and not exists (select 1 from chapters ch where ch.course_code=r.course_code and ch.department_id=r.department_id and ch.chapter_no=r.lesson_no and ch.deleted_at is null)
group by 1,2,3 order by 1,2;

\echo '== 4d. metadata.chapter vs lesson_no disagreement (notes)'
select count(*) filter (where metadata->>'chapter' is not null and metadata->>'chapter' ~ '^\d+$' and (metadata->>'chapter')::int<>lesson_no) disagree,
       count(*) filter (where metadata->>'chapter' is not null) has_meta_chapter
from resources where department_id=:dept and deleted_at is null and type='note';

\echo '== 5. Duplicates'
select 'course_code dup' kind, course_code, count(*) from courses where department_id=:dept and deleted_at is null group by course_code having count(*)>1
union all
select 'chapter_no dup', course_code||' #'||chapter_no, count(*) from chapters where department_id=:dept and deleted_at is null group by course_code, chapter_no having count(*)>1;

\echo '== 5b. Chapters whose course_code has no course'
select ch.course_code, count(*) from chapters ch where department_id=:dept and deleted_at is null
 and not exists (select 1 from courses c where c.course_code=ch.course_code and c.department_id=ch.department_id and c.deleted_at is null) group by 1;

\echo '== 5c. Courses with no level'
select count(*) from courses where department_id=:dept and deleted_at is null and level_id is null;

\echo '== 6. Batches: levels and students'
select b.name, b.is_studying,
 (select string_agg(l.name, ', ' order by l."order") from level_batches lb join levels l on l.id=lb.level_id and l.deleted_at is null where lb.batch_id=b.id) levels,
 (select count(*) from students s where s.batch_id=b.id and s.deleted_at is null) students,
 (select count(*) from resource_batches rb join resources r on r.id=rb.resource_id and r.deleted_at is null where rb.batch_id=b.id) tagged_resources
from batches b where b.department_id=:dept and b.deleted_at is null order by b.name;

\echo '== 7. Resource batch-tag classes'
with nb as (select count(*) n from batches where department_id=:dept and deleted_at is null),
t as (select r.id, r.type, (select count(*) from resource_batches rb join batches b on b.id=rb.batch_id and b.deleted_at is null where rb.resource_id=r.id) k
      from resources r where department_id=:dept and deleted_at is null)
select type,
 count(*) filter (where k=0) untagged,
 count(*) filter (where k>0 and k<(select n from nb)) some_batches,
 count(*) filter (where k>=(select n from nb)) all_batches
from t group by 1 order by 1;

\echo '== 7b. Tag-count distribution'
select k batches_tagged, count(*) from (select (select count(*) from resource_batches rb where rb.resource_id=r.id) k from resources r where department_id=:dept and deleted_at is null) x group by 1 order by 1;

\echo '== 8. Chapter batch tags'
select count(*) total, count(*) filter (where not exists(select 1 from chapter_batches cb where cb.chapter_id=ch.id)) untagged from chapters ch where department_id=:dept and deleted_at is null;

\echo '== 9. Things that point at resources'
select (select count(*) from bookmarks where entity_type='resource' and deleted_at is null) bookmarks,
       (select count(*) from resource_ratings) ratings;

\echo '== I. Notes per ambiguity class (after merging same-title chapter copies)'
with chn as (select c.*, regexp_replace(lower(c.chapter_title), '[^a-z]', '', 'g') nt from chapters c where department_id=:dept and deleted_at is null),
m as (
 select r.id,
  (select count(*) from chn ch where ch.course_code=r.course_code and ch.chapter_no=r.lesson_no) copies,
  (select count(distinct nt) from chn ch where ch.course_code=r.course_code and ch.chapter_no=r.lesson_no) titles_all,
  (select count(distinct nt) from chn ch where ch.course_code=r.course_code and ch.chapter_no=r.lesson_no
     and exists (select 1 from chapter_batches cb join resource_batches rb on rb.batch_id=cb.batch_id where cb.chapter_id=ch.id and rb.resource_id=r.id)) k
 from resources r where r.department_id=:dept and r.deleted_at is null and r.type='note')
select count(*) total_notes, count(*) filter (where copies=1) single_chapter, count(*) filter (where copies>1 and titles_all=1) dup_same_title,
 count(*) filter (where titles_all>1 and k=1) resolved_by_batch, count(*) filter (where k>1) ambiguous, count(*) filter (where titles_all>1 and k=0) no_overlap from m;

\echo '== J. (batch, course, chapter_no) that sees more than one chapter copy'
select count(*) from (select ch.course_code, ch.chapter_no, cb.batch_id from chapters ch join chapter_batches cb on cb.chapter_id=ch.id where ch.department_id=:dept and ch.deleted_at is null group by 1,2,3 having count(*)>1) x;

\echo '== N. Distinct chapter lists per course across batches'
with bc as (
 select ch.course_code, b.name bn, string_agg(ch.chapter_no||':'||regexp_replace(lower(ch.chapter_title),'[^a-z]','','g'), '|' order by ch.chapter_no, ch.chapter_title) lst
 from chapters ch join chapter_batches cb on cb.chapter_id=ch.id join batches b on b.id=cb.batch_id where ch.department_id=:dept and ch.deleted_at is null group by 1,2)
select versions, count(*) courses, string_agg(course_code, ' ' order by course_code) from (select course_code, count(distinct lst) versions from bc group by 1) x group by 1 order by 1;
