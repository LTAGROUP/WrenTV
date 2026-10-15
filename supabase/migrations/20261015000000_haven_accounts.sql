-- Haven accounts: profiles, servers, sharing, invites and device linking.
--
-- Clients (apps) talk to these tables directly under RLS.
-- Media servers authenticate with a per-server secret through the
-- security-definer RPCs below (server_heartbeat, server_access); the secret's
-- hash lives in private.server_secrets, which no client role can read.

create schema if not exists private;
revoke all on schema private from public, anon, authenticated;
grant usage on schema private to authenticated;

-- ---------------------------------------------------------------- profiles

create table public.profiles (
  id uuid primary key references auth.users (id) on delete cascade,
  display_name text not null default '',
  created_at timestamptz not null default now()
);
alter table public.profiles enable row level security;

create policy "profiles: read own" on public.profiles
  for select to authenticated using ((select auth.uid()) = id);
create policy "profiles: update own" on public.profiles
  for update to authenticated using ((select auth.uid()) = id) with check ((select auth.uid()) = id);

create function private.handle_new_user()
returns trigger
language plpgsql
security definer
set search_path = ''
as $$
begin
  insert into public.profiles (id, display_name)
  values (new.id, coalesce(new.raw_user_meta_data ->> 'display_name', split_part(new.email, '@', 1)));
  return new;
end;
$$;

create trigger on_auth_user_created
  after insert on auth.users
  for each row execute function private.handle_new_user();

-- ----------------------------------------------------------------- servers

create table public.servers (
  id uuid primary key default gen_random_uuid(),
  owner_id uuid not null references auth.users (id) on delete cascade,
  machine_id text not null unique,
  name text not null,
  local_urls text[] not null default '{}',
  public_url text,
  version text,
  -- [{id, name, type}] published by the server for sharing UIs
  libraries jsonb not null default '[]'::jsonb,
  last_seen_at timestamptz,
  created_at timestamptz not null default now()
);
create index servers_owner_id_idx on public.servers (owner_id);
alter table public.servers enable row level security;

create table private.server_secrets (
  server_id uuid primary key references public.servers (id) on delete cascade,
  secret_hash text not null
);

create table public.server_shares (
  server_id uuid not null references public.servers (id) on delete cascade,
  user_id uuid not null references auth.users (id) on delete cascade,
  -- null = every library on the server
  library_ids text[],
  created_at timestamptz not null default now(),
  primary key (server_id, user_id)
);
create index server_shares_user_id_idx on public.server_shares (user_id);
alter table public.server_shares enable row level security;

-- Helpers used by policies (private schema is not exposed over the API).
create function private.owns_server(p_server_id uuid)
returns boolean
language sql
stable
security definer
set search_path = ''
as $$
  select exists (select 1 from public.servers s where s.id = p_server_id and s.owner_id = (select auth.uid()));
$$;

create function private.shared_with_me(p_server_id uuid)
returns boolean
language sql
stable
security definer
set search_path = ''
as $$
  select exists (select 1 from public.server_shares sh where sh.server_id = p_server_id and sh.user_id = (select auth.uid()));
$$;

revoke execute on function private.owns_server(uuid), private.shared_with_me(uuid) from public, anon;
grant execute on function private.owns_server(uuid), private.shared_with_me(uuid) to authenticated;

create policy "servers: owner or shared can read" on public.servers
  for select to authenticated
  using (owner_id = (select auth.uid()) or (select private.shared_with_me(id)));
create policy "servers: owner can rename" on public.servers
  for update to authenticated
  using (owner_id = (select auth.uid())) with check (owner_id = (select auth.uid()));
create policy "servers: owner can remove" on public.servers
  for delete to authenticated
  using (owner_id = (select auth.uid()));
-- Inserts happen only through claim_server().

create policy "shares: owner or member can read" on public.server_shares
  for select to authenticated
  using (user_id = (select auth.uid()) or (select private.owns_server(server_id)));
create policy "shares: owner manages" on public.server_shares
  for all to authenticated
  using ((select private.owns_server(server_id)))
  with check ((select private.owns_server(server_id)));
create policy "shares: member can leave" on public.server_shares
  for delete to authenticated
  using (user_id = (select auth.uid()));

-- ----------------------------------------------------------------- invites

create table public.share_invites (
  id uuid primary key default gen_random_uuid(),
  server_id uuid not null references public.servers (id) on delete cascade,
  email text not null check (email = lower(email)),
  library_ids text[],
  invited_by uuid not null default auth.uid() references auth.users (id) on delete cascade,
  created_at timestamptz not null default now(),
  unique (server_id, email)
);
create index share_invites_email_idx on public.share_invites (email);
alter table public.share_invites enable row level security;

create policy "invites: owner manages" on public.share_invites
  for all to authenticated
  using ((select private.owns_server(server_id)))
  with check ((select private.owns_server(server_id)));
create policy "invites: invitee can read" on public.share_invites
  for select to authenticated
  using (email = lower((select auth.jwt() ->> 'email')));

-- Turns pending invites for the caller's email into shares.
create function public.accept_invites()
returns integer
language plpgsql
security definer
set search_path = ''
as $$
declare
  v_uid uuid := (select auth.uid());
  v_email text := lower((select auth.jwt() ->> 'email'));
  v_count integer;
begin
  if v_uid is null or v_email is null then
    raise exception 'not signed in';
  end if;
  insert into public.server_shares (server_id, user_id, library_ids)
  select i.server_id, v_uid, i.library_ids
  from public.share_invites i
  join public.servers s on s.id = i.server_id
  where i.email = v_email and s.owner_id <> v_uid
  on conflict (server_id, user_id) do update set library_ids = excluded.library_ids;
  get diagnostics v_count = row_count;
  delete from public.share_invites where email = v_email;
  return v_count;
end;
$$;
revoke execute on function public.accept_invites() from public, anon;
grant execute on function public.accept_invites() to authenticated;

-- ------------------------------------------------------- server-facing RPCs

-- Called by the app on behalf of a signed-in user to register a server they run.
create function public.claim_server(p_machine_id text, p_name text, p_secret text)
returns uuid
language plpgsql
security definer
set search_path = ''
as $$
declare
  v_uid uuid := (select auth.uid());
  v_server public.servers;
begin
  if v_uid is null then
    raise exception 'not signed in';
  end if;
  if length(p_secret) < 32 then
    raise exception 'secret too short';
  end if;
  select * into v_server from public.servers where machine_id = p_machine_id;
  if found then
    if v_server.owner_id <> v_uid then
      raise exception 'server is already claimed by another account';
    end if;
    update public.servers set name = p_name where id = v_server.id;
  else
    insert into public.servers (owner_id, machine_id, name)
    values (v_uid, p_machine_id, p_name)
    returning * into v_server;
  end if;
  insert into private.server_secrets (server_id, secret_hash)
  values (v_server.id, encode(extensions.digest(p_secret, 'sha256'), 'hex'))
  on conflict (server_id) do update set secret_hash = excluded.secret_hash;
  return v_server.id;
end;
$$;
revoke execute on function public.claim_server(text, text, text) from public, anon;
grant execute on function public.claim_server(text, text, text) to authenticated;

create function private.check_server_secret(p_server_id uuid, p_secret text)
returns boolean
language sql
stable
security definer
set search_path = ''
as $$
  select exists (
    select 1 from private.server_secrets
    where server_id = p_server_id
      and secret_hash = encode(extensions.digest(p_secret, 'sha256'), 'hex')
  );
$$;
revoke execute on function private.check_server_secret(uuid, text) from public, anon, authenticated;

create function public.server_heartbeat(
  p_server_id uuid, p_secret text, p_name text, p_local_urls text[], p_public_url text, p_version text, p_libraries jsonb
)
returns void
language plpgsql
security definer
set search_path = ''
as $$
begin
  if not private.check_server_secret(p_server_id, p_secret) then
    raise exception 'invalid server credentials';
  end if;
  update public.servers
  set name = coalesce(nullif(p_name, ''), name),
      local_urls = coalesce(p_local_urls, '{}'),
      public_url = nullif(p_public_url, ''),
      version = p_version,
      libraries = coalesce(p_libraries, '[]'::jsonb),
      last_seen_at = now()
  where id = p_server_id;
end;
$$;
revoke execute on function public.server_heartbeat(uuid, text, text, text[], text, text, jsonb) from public;
grant execute on function public.server_heartbeat(uuid, text, text, text[], text, text, jsonb) to anon, authenticated;

-- What may this user do on this server? role is 'owner', 'shared' or no row.
create function public.server_access(p_server_id uuid, p_secret text, p_user_id uuid)
returns table (role text, library_ids text[])
language plpgsql
stable
security definer
set search_path = ''
as $$
begin
  if not private.check_server_secret(p_server_id, p_secret) then
    raise exception 'invalid server credentials';
  end if;
  return query
    select 'owner'::text, null::text[] from public.servers s where s.id = p_server_id and s.owner_id = p_user_id
    union all
    select 'shared'::text, sh.library_ids from public.server_shares sh where sh.server_id = p_server_id and sh.user_id = p_user_id
    limit 1;
end;
$$;
revoke execute on function public.server_access(uuid, text, uuid) from public;
grant execute on function public.server_access(uuid, text, uuid) to anon, authenticated;

-- ----------------------------------------------------------- device linking
-- A new device shows a short code; a signed-in device approves it (via the
-- link-device edge function, which mints a one-time sign-in token); the new
-- device polls and exchanges that token for its own session.

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
  loop
    v_code := '';
    for i in 1..6 loop
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
