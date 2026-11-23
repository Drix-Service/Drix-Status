import { api, type Incident } from "../api";

/** Open incidents and maintenance (shared by the tab badge and the Incidents screen). */
export const openIncidentsQuery = {
  queryKey: ["m-incidents", "open"],
  queryFn: () => api.get<Incident[]>("/incidents?state=open&limit=50"),
  refetchInterval: 30_000,
};
