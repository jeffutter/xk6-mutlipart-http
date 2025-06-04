import multipartHTTP from 'k6/x/multipartHTTP';
import { setTimeout, clearTimeout, setInterval, clearInterval } from "k6/timers"
import { check, sleep } from 'k6';

const sessionDuration = 10000;

export const options = {
  // discardResponseBodies: true,
  scenarios: {
    contacts: {
      executor: 'constant-vus',
      vus: 200,
      duration: '90s',
      gracefulStop: '3s',
    },
  },
};

export default function() {

  var state = multipartHTTP.getInternalState();

  // var totalVus = state.activeVUs;
  var currentVu = state.vuID;

  console.log(
    `Active VUs: ${state.activeVUs}, Iteration: ${state.iteration}, VU ID: ${state.vuID}, VU ID from runtime: ${state.vuIDFromRuntime}`
  );

  var variables = [
  ];

  var numbOfVariables = variables.length;
  var variable = variables[currentVu % numbOfVariables]

  let subscription = `subscription { time { time } }`;

  let payload = {
    query: subscription,
    variables: variable,
  };

  var url = "http://localhost:8080/query";
  var params = {
    method: 'POST',
    body: JSON.stringify(payload),
    headers: {
      "Accept": 'multipart/mixed; boundary="graphql"; subscriptionSpec=1.0, application/json',
      "content-type": 'application/json',
    }
  };

  var sub = multipartHTTP.MultipartHttp(url, params);

  sub.addEventListener("open", () => {
    console.log("VU ID: " + currentVu + " JS OPEN");

    sub.addEventListener("message", function(event) {
      // console.log("VU ID Message: " + currentVu + " " + JSON.stringify(event));
      // console.log("VU ID Message: " + currentVu + " " + JSON.stringify(JSON.parse(event.data)));
      // console.log("VU ID: " + currentVu + " " + JSON.stringify(JSON.parse(new TextDecoder().decode(event.data))));
    });

    sub.addEventListener("close", function(event) {
      console.log("VU ID close: " + currentVu + " " + JSON.stringify(event));
    });

    sub.addEventListener("error", function(event) {
      console.log("VU ID error: " + currentVu + " " + JSON.stringify(event));
    });
  });


  let timeoutId = setTimeout(function() {
    console.log("Closing Subscription");
    sub.close();
    console.log("After Closing Subscription");
    clearTimeout(timeoutId);
    console.log("After Sleep");
  }, 1000);

  // check(response, { "status is 200": (r) => r && r.status === 200 });
}
