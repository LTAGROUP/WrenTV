-- Device linking (short codes approved from another device) is gone: every
-- app signs in with the account's email and password.

drop function if exists public.create_device_link(text, text);
drop function if exists public.poll_device_link(text, text);
drop function if exists public.device_link_info(text);
drop function if exists public.approve_device_link(text, uuid, text);
drop table if exists private.device_links;
