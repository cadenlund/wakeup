import { Link } from "expo-router";
import * as WebBrowser from "expo-web-browser";
import React from "react";
import { Platform } from "react-native";

export function ExternalLink(
  props: Omit<React.ComponentProps<typeof Link>, "href"> & { href: string },
) {
  return (
    <Link
      target="_blank"
      {...props}
      // expo-router types Link.href as a strictly enumerated union
      // built from the file-based route table, so a runtime-only URL
      // string fails the constraint. The cast is the documented escape
      // hatch — see expo-router/typed-routes notes.
      href={props.href as never}
      onPress={(e) => {
        if (Platform.OS !== "web") {
          // Prevent the default behavior of linking to the default browser on native.
          e.preventDefault();
          // Open the link in an in-app browser.
          WebBrowser.openBrowserAsync(props.href as string);
        }
      }}
    />
  );
}
