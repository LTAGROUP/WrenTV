-- Automatic HTTPS addresses (wrentv.net). Each claimed server gets a random
-- direct_id; the wrentv-direct Worker publishes DNS records under
-- <direct_id>.wrentv.net for it (A records named after each IP, and the ACME
-- DNS-01 TXT record) after checking the server's secret through
-- direct_authorize(). The server then holds a wildcard cert for
-- *.<direct_id>.wrentv.net and publishes https addresses in its heartbeat.

alter table public.servers
  add column direct_id text unique,
  -- https://<public-ip>.<direct_id>.wrentv.net:<port>, set only when the
  -- server confirmed it is reachable from the internet.
  add column remote_url text;

-- Owners may rename servers; every other column is written by the server's
-- own RPCs (so an owner can't point direct_id at another server's records).
revoke update on public.servers from authenticated;
grant update (name) on public.servers to authenticated;

create table private.direct_calls (
  server_id uuid not null references public.servers (id) on delete cascade,
  at timestamptz not null default now()
);
create index direct_calls_server_at_idx on private.direct_calls (server_id, at);

-- Called by the Worker with the credentials the server sent it. Returns the
-- server's direct_id (assigning one on first use). Limited to 60 calls an hour.
create function public.direct_authorize(p_server_id uuid, p_secret text)
returns text
language plpgsql
volatile
security definer
set search_path = ''
as $$
declare
  v_id text;
begin
  if not private.check_server_secret(p_server_id, p_secret) then
    raise exception 'invalid server credentials';
  end if;
  delete from private.direct_calls where at < now() - interval '1 hour';
  if (select count(*) from private.direct_calls where server_id = p_server_id) >= 60 then
    raise exception 'rate limited';
  end if;
  insert into private.direct_calls (server_id) values (p_server_id);

  select direct_id into v_id from public.servers where id = p_server_id;
  if v_id is null then
    v_id := encode(extensions.gen_random_bytes(16), 'hex');
    update public.servers set direct_id = v_id where id = p_server_id;
  end if;
  return v_id;
end;
$$;
revoke execute on function public.direct_authorize(uuid, text) from public;
grant execute on function public.direct_authorize(uuid, text) to anon, authenticated;

-- Heartbeat gains the automatic remote address. The default keeps older
-- servers (which don't send it) working.
drop function public.server_heartbeat(uuid, text, text, text[], text, text, jsonb);
create function public.server_heartbeat(
  p_server_id uuid, p_secret text, p_name text, p_local_urls text[], p_public_url text, p_version text, p_libraries jsonb,
  p_remote_url text default null
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
      remote_url = nullif(p_remote_url, ''),
      version = p_version,
      libraries = coalesce(p_libraries, '[]'::jsonb),
      last_seen_at = now()
  where id = p_server_id;
end;
$$;
revoke execute on function public.server_heartbeat(uuid, text, text, text[], text, text, jsonb, text) from public;
grant execute on function public.server_heartbeat(uuid, text, text, text[], text, text, jsonb, text) to anon, authenticated;
