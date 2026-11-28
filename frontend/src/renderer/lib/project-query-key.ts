/**
 * The project detail query, keyed by the daemon that holds it: the local
 * daemon's projects under ["project", id], a remote host's under
 * ["project", hostId, id], so pages of the same project on different hosts
 * never share a cache entry.
 */
export const projectQueryKey = (id: string, hostId?: string) => hostId ? ["project", hostId, id] as const : ["project", id] as const;
