-- Device linking, back for TVs: typing an email and password with a remote is
-- slow, so a TV shows a short code (and a QR code of
-- https://watch.wrentv.com/link?code=…) before it knows any server. A signed-in
-- phone or browser approves it through the link-device edge function, which
-- mints a one-time sign-in token; the TV polls and exchanges that token for its
-- own session (POST /auth/v1/verify { type: "magiclink", token_hash }).

create table private.device_links (
  code text primary key,
  device_secret_hash text not null,
  device_name text not null default '',
  platform text not null default '',
  user_id uuid references auth.users (id) on delete cascade,
  token_hash text,
  expires_at timestamptz not null,
  created_at timestamptz not null default now()
);

create function public.create_device_link(p_device_name text, p_platform text)
returns table (code text, device_secret text, expires_at timestamptz)
language plpgsql
volatile
security definer
set search_path = ''
as $$
declare
  v_code text;
  v_secret text := encode(extensions.gen_random_bytes(24), 'hex');
  v_alphabet text := 'ABCDEFGHJKLMNPQRSTUVWXYZ23456789';
  v_expires timestamptz := now() + interval '10 minutes';
  i integer;
begin
  delete from private.device_links where device_links.expires_at < now();
  if (select count(*) from private.device_links) >= 1000 then
    raise exception 'too many sign-in codes waiting; try again in a few minutes';
  end if;
  loop
    v_code := '';
    for i in 1..8 loop
      v_code := v_code || substr(v_alphabet, 1 + (get_byte(extensions.gen_random_bytes(1), 0) % 32), 1);
    end loop;
    exit when not exists (select 1 from private.device_links d where d.code = v_code);
  end loop;
  insert into private.device_links (code, device_secret_hash, device_name, platform, expires_at)
  values (v_code, encode(extensions.digest(v_secret, 'sha256'), 'hex'), left(coalesce(p_device_name, ''), 100),
          left(coalesce(p_platform, ''), 40), v_expires);
  return query select v_code, v_secret, v_expires;
end;
$$;
revoke execute on function public.create_device_link(text, text) from public;
grant execute on function public.create_device_link(text, text) to anon, authenticated;

-- Returns status 'pending' | 'approved' (with a one-time token_hash) | 'expired'.
create function public.poll_device_link(p_code text, p_device_secret text)
returns table (status text, token_hash text)
language plpgsql
volatile
security definer
set search_path = ''
as $$
declare
  v_link private.device_links;
begin
  select * into v_link from private.device_links d
  where d.code = upper(p_code)
    and d.device_secret_hash = encode(extensions.digest(p_device_secret, 'sha256'), 'hex');
  if not found or v_link.expires_at < now() then
    return query select 'expired'::text, null::text;
    return;
  end if;
  if v_link.token_hash is null then
    return query select 'pending'::text, null::text;
    return;
  end if;
  delete from private.device_links d where d.code = v_link.code;
  return query select 'approved'::text, v_link.token_hash;
end;
$$;
revoke execute on function public.poll_device_link(text, text) from public;
grant execute on function public.poll_device_link(text, text) to anon, authenticated;

-- Used by the link-device edge function (service role) only.
create function public.device_link_info(p_code text)
returns table (device_name text, platform text, claimed boolean)
language sql
stable
security definer
set search_path = ''
as $$
  select d.device_name, d.platform, d.user_id is not null
  from private.device_links d where d.code = upper(p_code) and d.expires_at > now();
$$;
revoke execute on function public.device_link_info(text) from public, anon, authenticated;
grant execute on function public.device_link_info(text) to service_role;

create function public.approve_device_link(p_code text, p_user_id uuid, p_token_hash text)
returns boolean
language plpgsql
volatile
security definer
set search_path = ''
as $$
begin
  update private.device_links
  set user_id = p_user_id, token_hash = p_token_hash
  where code = upper(p_code) and expires_at > now() and user_id is null;
  return found;
end;
$$;
revoke execute on function public.approve_device_link(text, uuid, text) from public, anon, authenticated;
grant execute on function public.approve_device_link(text, uuid, text) to service_role;
