-- Server tickets: how an app signs in to one media server without handing
-- that server the account's own token.
--
-- The account token works everywhere: on the account service and on every
-- server the account can use. A media server is run by whoever owns it, so it
-- must never see that token. Instead the app asks for a ticket for one server
-- (issue_server_ticket, as the signed-in user) and gives the ticket to that
-- server, which redeems it with its own secret (redeem_server_ticket) to learn
-- who is signing in, then issues its own token that only it accepts.
--
-- A ticket is random, works once, for one server, for two minutes. Only its
-- hash is stored. A server other than the one it names can't redeem it, so a
-- ticket handed to the wrong machine is worth nothing.

create table private.server_tickets (
  ticket_hash text primary key,
  server_id uuid not null references public.servers (id) on delete cascade,
  user_id uuid not null references auth.users (id) on delete cascade,
  expires_at timestamptz not null,
  created_at timestamptz not null default now()
);
create index server_tickets_user_idx on private.server_tickets (user_id);
create index server_tickets_expires_idx on private.server_tickets (expires_at);

-- Called by an app as the signed-in user. Only the server's owner and the
-- accounts it is shared with can get a ticket for it.
create function public.issue_server_ticket(p_server_id uuid)
returns text
language plpgsql
volatile
security definer
set search_path = ''
as $$
declare
  v_uid uuid := (select auth.uid());
  v_ticket text := encode(extensions.gen_random_bytes(32), 'hex');
begin
  if v_uid is null then
    raise exception 'not signed in';
  end if;
  if not exists (
    select 1 from public.servers s where s.id = p_server_id and s.owner_id = v_uid
    union all
    select 1 from public.server_shares sh where sh.server_id = p_server_id and sh.user_id = v_uid
  ) then
    raise exception 'this account doesn''t have access to that server';
  end if;
  delete from private.server_tickets where expires_at < now();
  -- An app asks for one ticket per sign-in; this is far above normal use.
  if (select count(*) from private.server_tickets t where t.user_id = v_uid) >= 60 then
    raise exception 'too many sign-ins in progress; try again in a couple of minutes';
  end if;
  insert into private.server_tickets (ticket_hash, server_id, user_id, expires_at)
  values (encode(extensions.digest(v_ticket, 'sha256'), 'hex'), p_server_id, v_uid, now() + interval '2 minutes');
  return v_ticket;
end;
$$;
revoke execute on function public.issue_server_ticket(uuid) from public, anon;
grant execute on function public.issue_server_ticket(uuid) to authenticated;

-- Called by a media server with its own secret. Returns who the ticket was
-- issued to (no row when it is unknown, used, expired or for another server)
-- and uses it up.
create function public.redeem_server_ticket(p_server_id uuid, p_secret text, p_ticket text)
returns table (user_id uuid, email text, display_name text)
language plpgsql
volatile
security definer
set search_path = ''
as $$
declare
  v_uid uuid;
begin
  if not private.check_server_secret(p_server_id, p_secret) then
    raise exception 'invalid server credentials';
  end if;
  delete from private.server_tickets t
  where t.ticket_hash = encode(extensions.digest(p_ticket, 'sha256'), 'hex')
    and t.server_id = p_server_id
    and t.expires_at > now()
  returning t.user_id into v_uid;
  if v_uid is null then
    return;
  end if;
  return query
    select u.id, coalesce(u.email, '')::text,
           coalesce(nullif(p.display_name, ''), u.raw_user_meta_data ->> 'display_name', '')::text
    from auth.users u
    left join public.profiles p on p.id = u.id
    where u.id = v_uid;
end;
$$;
revoke execute on function public.redeem_server_ticket(uuid, text, text) from public;
grant execute on function public.redeem_server_ticket(uuid, text, text) to anon, authenticated;
