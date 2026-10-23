import { createClient } from "@connectrpc/connect";
import { createConnectTransport } from "@connectrpc/connect-web";
import { PushService, SessionService } from "../gen/jcodeweb/v1/jcodeweb_pb";

const transport = createConnectTransport({ baseUrl: location.origin });

export const sessions = createClient(SessionService, transport);
export const pushApi = createClient(PushService, transport);
