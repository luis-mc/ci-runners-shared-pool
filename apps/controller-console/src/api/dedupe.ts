// One request for the same answer (audit R3-10). A page load asks for the machine list from four components and for
// the repositories from three; the screens do not know about each other, so the API wrapper does: a GET that is
// already on its way is not sent again, its answer is shared. Nothing is kept once the answer has arrived, so a reload
// after an event or a change always reads fresh data; any change (POST, PATCH, PUT, DELETE) is sent as it is.

type FetchLike = (input: Request) => Promise<Response>;

/** A fetch that gives every caller of the same GET, made while it is in flight, the one answer. */
export function dedupeFetch(base: FetchLike = (r) => globalThis.fetch(r)): typeof fetch {
  const inFlight = new Map<string, Promise<Response>>();
  const send: FetchLike = (r) => base(r);
  const dedupe = (input: RequestInfo | URL, init?: RequestInit): Promise<Response> => {
    const request =
      input instanceof Request && init === undefined ? input : new Request(input, init);
    if (request.method !== 'GET' || request.signal.aborted) return send(request);
    const key = request.url;
    const shared = inFlight.get(key);
    if (shared) {
      // The first caller may be cancelled; a follower then asks for itself instead of failing with it.
      return shared.then(
        (res) => res.clone(),
        () => send(request),
      );
    }
    const mine = send(request.clone());
    inFlight.set(key, mine);
    const forget = () => {
      if (inFlight.get(key) === mine) inFlight.delete(key);
    };
    mine.then(forget, forget);
    return mine.then((res) => res.clone());
  };
  return dedupe;
}
