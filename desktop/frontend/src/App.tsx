import { useState, useEffect } from 'react'
import {Events, WML} from "@wailsio/runtime";
import {FeatureService, GreetService} from "../bindings/github.com/mrgeoffrich/bacio/desktop";

function App() {
  const [name, setName] = useState<string>('');
  const [result, setResult] = useState<string>('Please enter your name below 👇');
  const [time, setTime] = useState<string>('Listening for Time event...');
  const [featureSummary, setFeatureSummary] = useState<string>('Loading features…');

  const doGreet = () => {
    let localName = name;
    if (!localName) {
      localName = 'anonymous';
    }
    GreetService.Greet(localName).then((resultValue: string) => {
      setResult(resultValue);
    }).catch((err: any) => {
      console.log(err);
    });
  }

  useEffect(() => {
    Events.On('time', (timeValue: any) => {
      setTime(timeValue.data);
    });
    FeatureService.List().then((features) => {
      console.log('features from bacio store:', features);
      const count = features?.length ?? 0;
      setFeatureSummary(`Found ${count} feature${count === 1 ? '' : 's'} in the local bacio store`);
    }).catch((err: any) => {
      console.error('FeatureService.List failed:', err);
      setFeatureSummary(`FeatureService.List failed: ${err?.message ?? err}`);
    });
    // Reload WML so it picks up the wml tags
    WML.Reload();
  }, []);

  return (
    <div className="container">
      <div>
        <a data-wml-openURL="https://wails.io">
          <img src="/wails.png" className="logo" alt="Wails logo"/>
        </a>
        <a data-wml-openURL="https://reactjs.org">
          <img src="/react.svg" className="logo react" alt="React logo"/>
        </a>
      </div>
      <h1>Wails + React</h1>
      <div className="result">{result}</div>
      <div className="card">
        <div className="input-box">
          <input className="input" value={name} onChange={(e) => setName(e.target.value)} type="text" autoComplete="off"/>
          <button className="btn" onClick={doGreet}>Greet</button>
        </div>
      </div>
      <div className="footer">
        <div><p>Click on the Wails logo to learn more</p></div>
        <div><p>{time}</p></div>
        <div><p>{featureSummary}</p></div>
      </div>
    </div>
  )
}

export default App
