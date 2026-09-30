-- =====================================================================
-- KaziWise LMS - optional demo seed data.
-- Run ONLY on a throwaway database. It creates one organisation, one
-- user per role, one published course with 3 modules / 5 lessons /
-- 10 questions, and one active campaign.
--
-- Password for every seeded account: "Password123!"
-- This script only writes the profiles. Run the API with SEED_DEMO=true
-- (and SEED_AUTH=true) so the matching credentials are created too; under
-- Supabase that step also provisions the auth users and back-links them.
-- =====================================================================

insert into organisations (name, slug, country, is_demo)
values ('KaziWise Demo Ltd', 'kaziwise-demo', 'Kenya', true)
on conflict (slug) do nothing;

create or replace function kz_demo_org() returns uuid language sql as $$
  select id from organisations where slug = 'kaziwise-demo'
$$;

create or replace function kz_seed_profile(
  p_email text, p_name text, p_role user_role, p_dept text, p_emp text, p_title text
) returns uuid language sql as $$
  with upserted as (
    insert into profiles (org_id, email, full_name, role, department, employee_number, job_title)
    values (kz_demo_org(), p_email, p_name, p_role, p_dept, p_emp, p_title)
    on conflict (org_id, email) do update set full_name = excluded.full_name
    returning id
  )
  select id from upserted
$$;

select kz_seed_profile('superadmin@kaziwise.dev', 'Sam Super',  'super_admin', 'Platform',      'EMP-0001', 'Platform Owner');
select kz_seed_profile('admin@kaziwise.dev',     'Ada Admin',  'org_admin',   'Operations',    'EMP-0002', 'L&D Manager');
select kz_seed_profile('manager@kaziwise.dev',   'Musa Manager','manager',    'Operations',    'EMP-0003', 'Team Lead');
select kz_seed_profile('learner@kaziwise.dev',   'Lena Learner','learner',    'Operations',    'EMP-0004', 'Officer');
select kz_seed_profile('learner2@kaziwise.dev',  'Kofi Learner','learner',    'Finance',       'EMP-0005', 'Accountant');
select kz_seed_profile('learner3@kaziwise.dev',  'Achi Learner','learner',    'Sales',         'EMP-0006', 'Account Executive');

-- Attach every learner in Operations to the manager
update profiles set manager_id = (select id from profiles where email = 'manager@kaziwise.dev')
where org_id = kz_demo_org() and email in ('learner@kaziwise.dev', 'learner2@kaziwise.dev');

insert into departments (org_id, name)
select kz_demo_org(), d from (values ('Operations'), ('Finance'), ('Sales'), ('Human Resources'), ('Platform')) as t(d)
on conflict (org_id, name) do nothing;

-- ---- Course: "Workplace Safety Essentials" -----------------------
with c as (
  insert into courses (org_id, title, code, description, category, status, estimated_minutes, published_at)
  values (kz_demo_org(), 'Workplace Safety Essentials', 'WSE-101',
          'Mandatory safety induction covering fire, first aid and incident reporting.',
          'Compliance', 'published', 45, now())
  on conflict (org_id, code) do update set title = excluded.title
  returning id
), m1 as (
  insert into modules (course_id, org_id, title, position)
  select c.id, kz_demo_org(), 'Getting Started', 1 from c returning id
), m2 as (
  insert into modules (course_id, org_id, title, position)
  select c.id, kz_demo_org(), 'Emergency Response', 2 from c returning id
), m3 as (
  insert into modules (course_id, org_id, title, position)
  select c.id, kz_demo_org(), 'Reporting', 3 from c returning id
), l1 as (
  insert into lessons (module_id, course_id, org_id, title, position, estimated_minutes)
  select m1.id, c.id, kz_demo_org(), 'Welcome & Scope', 1, 5 from m1, c returning id
), l2 as (
  insert into lessons (module_id, course_id, org_id, title, position, estimated_minutes)
  select m2.id, c.id, kz_demo_org(), 'Fire Safety', 1, 15 from m2, c returning id
), l3 as (
  insert into lessons (module_id, course_id, org_id, title, position, estimated_minutes)
  select m2.id, c.id, kz_demo_org(), 'First Aid Basics', 2, 15 from m2, c returning id
), l4 as (
  insert into lessons (module_id, course_id, org_id, title, position, estimated_minutes)
  select m3.id, c.id, kz_demo_org(), 'Incident Reporting', 1, 10 from m3, c returning id
)
insert into blocks (lesson_id, org_id, type, position, title, body)
select l.id, kz_demo_org(), 'text', 1, 'Scope of this course',
       'This course covers the safety obligations every employee shares. Work through each chapter in order.'
from lessons l
join courses c on c.id = l.course_id
where c.code = 'WSE-101' and l.title = 'Welcome & Scope'
  and not exists (select 1 from blocks b where b.lesson_id = l.id and b.title = 'Scope of this course');

insert into blocks (lesson_id, org_id, type, position, title, body)
select l.id, kz_demo_org(), 'text', 1, 'Fire safety basics',
       'Know your nearest exit, the location of the fire blanket, and who the fire marshal is.'
from lessons l
join courses c on c.id = l.course_id
where c.code = 'WSE-101' and l.title = 'Fire Safety'
  and not exists (select 1 from blocks b where b.lesson_id = l.id and b.title = 'Fire safety basics');

insert into blocks (lesson_id, org_id, type, position, title, body)
select l.id, kz_demo_org(), 'text', 1, 'How to report',
       'Report every incident within 24 hours using the internal incident form.'
from lessons l
join courses c on c.id = l.course_id
where c.code = 'WSE-101' and l.title = 'Incident Reporting'
  and not exists (select 1 from blocks b where b.lesson_id = l.id and b.title = 'How to report');

-- ---- 10 assessment questions, one of each supported type -------
insert into questions (course_id, org_id, type, prompt, points, position, min_length, max_length)
values
  ((select id from courses where code = 'WSE-101'), kz_demo_org(), 'single_choice', 'Where is the emergency exit assembly point?', 2, 1, null, null),
  ((select id from courses where code = 'WSE-101'), kz_demo_org(), 'single_choice', 'How often should fire extinguishers be inspected?', 2, 2, null, null),
  ((select id from courses where code = 'WSE-101'), kz_demo_org(), 'multi_choice',  'Which items belong in a first aid kit?', 3, 3, null, null),
  ((select id from courses where code = 'WSE-101'), kz_demo_org(), 'multi_choice',  'Select every acceptable way to report an incident.', 3, 4, null, null),
  ((select id from courses where code = 'WSE-101'), kz_demo_org(), 'short_text',   'In one sentence, state who the fire marshal is.', 5, 5, 10, 200),
  ((select id from courses where code = 'WSE-101'), kz_demo_org(), 'short_text',   'Give the phone number used to report an incident.', 2, 6, 5, 40),
  ((select id from courses where code = 'WSE-101'), kz_demo_org(), 'long_text',    'Explain, in a short paragraph, what you would do if you discovered a fire.', 10, 7, 100, 2000),
  ((select id from courses where code = 'WSE-101'), kz_demo_org(), 'long_text',    'Describe the steps you would take to help an unconscious colleague.', 10, 8, 100, 2000),
  ((select id from courses where code = 'WSE-101'), kz_demo_org(), 'single_choice', 'How many fire exits should a floor have at minimum?', 2, 9, null, null),
  ((select id from courses where code = 'WSE-101'), kz_demo_org(), 'multi_choice',  'Which of these are reportable incidents?', 3, 10, null, null);

insert into question_options (question_id, org_id, label, is_correct, position)
select q.id, kz_demo_org(), o.label, o.is_correct, o.pos
from questions q
join (values
  ('Where is the emergency exit assembly point?', 'Car park opposite reception', true, 1),
  ('Where is the emergency exit assembly point?', 'The roof terrace', false, 2),
  ('Where is the emergency exit assembly point?', 'Inside the server room', false, 3),
  ('How often should fire extinguishers be inspected?', 'Monthly', true, 1),
  ('How often should fire extinguishers be inspected?', 'Every five years', false, 2),
  ('Which items belong in a first aid kit?', 'Plasters', true, 1),
  ('Which items belong in a first aid kit?', 'Bandages', true, 2),
  ('Which items belong in a first aid kit?', 'Antiseptic wipes', true, 3),
  ('Which items belong in a first aid kit?', 'Coffee sachets', false, 4),
  ('Select every acceptable way to report an incident.', 'Internal incident form', true, 1),
  ('Select every acceptable way to report an incident.', 'Email to the safety officer', true, 2),
  ('Select every acceptable way to report an incident.', 'Mentioning it on social media', false, 3),
  ('How many fire exits should a floor have at minimum?', 'Two', true, 1),
  ('How many fire exits should a floor have at minimum?', 'One', false, 2),
  ('Which of these are reportable incidents?', 'Near miss', true, 1),
  ('Which of these are reportable incidents?', 'Slip and fall injury', true, 2),
  ('Which of these are reportable incidents?', 'A broken coffee mug', false, 3)
) as o(qprompt, label, is_correct, pos) on o.qprompt = q.prompt;

-- ---- Campaign + assignments --------------------------------------
with camp as (
  insert into campaigns (org_id, course_id, name, description, status, audience_type, due_date,
                         pass_mark, issue_certificate, max_attempts, launched_at)
  select kz_demo_org(), id, 'Q3 Safety Induction', 'Company-wide safety induction for Q3.',
         'active', 'all', current_date + 30, 70, true, 3, now()
  from courses where code = 'WSE-101'
  on conflict do nothing
  returning id
)
insert into assignments (org_id, campaign_id, course_id, learner_id, status, due_date, lessons_total, lessons_done)
select kz_demo_org(), camp.id, co.id, p.id, 'not_started', current_date + 30, 4, 0
from camp, profiles p, courses co
where co.code = 'WSE-101' and p.org_id = kz_demo_org() and p.role = 'learner'
on conflict (campaign_id, learner_id) do nothing;
